package services

import (
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/pkg/errors"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	netwv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/networking/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cvewatcher/mulval/deploy/common"
	"github.com/cvewatcher/mulval/deploy/services/parts"
)

type (
	MulVal struct {
		pulumi.ResourceState

		// Parts
		ns    *parts.Namespace
		pgsql *parts.PostgreSQL
		nats  *parts.Nats
		mv    *parts.MulVal

		// Exposure
		svc *corev1.Service

		// Interface and ports network policies
		mvToPgsqlAndNats *netwv1.NetworkPolicy
		natsFromMv       *netwv1.NetworkPolicy

		// Outputs

		PodLabels pulumi.StringMapOutput
		Endpoint  pulumi.StringOutput
	}

	MulValArgs struct {
		Tag pulumi.StringPtrInput

		// Registry define from where to fetch the MulVal Docker images.
		// If set empty, defaults to Docker Hub.
		Registry pulumi.StringPtrInput

		// LogLevel defines the level at which to log.
		LogLevel pulumi.StringInput

		Namespace       pulumi.StringInput
		createNamespace bool

		// PVCAccessModes defines the access modes supported by the PVC.
		PVCAccessModes pulumi.StringArrayInput
		pvcAccessModes pulumi.StringArrayOutput

		// PVCStorageSize enable to configure the storage size of the PVC MulVal
		// will write into (store Pulumi stacks, data persistency, ...).
		// Default to 2Gi.
		// See https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#meaning-of-memory
		// for syntax.
		PVCStorageSize pulumi.StringInput

		PgToAPIServerTemplate pulumi.StringPtrInput

		PostgresOperatorNamespace pulumi.StringPtrInput

		StorageClassName pulumi.StringInput

		// RomeoClaimName, if set, will turn on the coverage export of MulVal for later download.
		RomeoClaimName pulumi.StringInput

		// Requests for the MulVal container. For more infos:
		// https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/
		Requests pulumi.StringMapInput

		// Limits for the MulVal container. For more infos:
		// https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/
		Limits pulumi.StringMapInput

		Swagger, UI bool

		OTel *common.OTelArgs
	}
)

func NewMulVal(ctx *pulumi.Context, name string, args *MulValArgs, opts ...pulumi.ResourceOption) (*MulVal, error) {
	mv := &MulVal{}

	args = mv.defaults(args)
	if err := ctx.RegisterComponentResource("cvewatcher:mulval:mulval", name, mv, opts...); err != nil {
		return nil, err
	}
	opts = append(opts, pulumi.Parent(mv))
	if err := mv.provision(ctx, args, opts...); err != nil {
		return nil, err
	}
	if err := mv.outputs(ctx); err != nil {
		return nil, err
	}
	return mv, nil
}

func (mv *MulVal) defaults(args *MulValArgs) *MulValArgs {
	if args == nil {
		args = &MulValArgs{}
	}

	args.createNamespace = args.Namespace == nil
	if args.Namespace != nil {
		wg := &sync.WaitGroup{}
		wg.Add(1)
		args.Namespace.ToStringOutput().ApplyT(func(ns string) error {
			args.createNamespace = ns == ""
			wg.Done()
			return nil
		})
		wg.Wait()
	}

	return args
}

func (mv *MulVal) provision(ctx *pulumi.Context, args *MulValArgs, opts ...pulumi.ResourceOption) (err error) {
	// Create namespace if required
	namespace := args.Namespace
	if args.createNamespace {
		mv.ns, err = parts.NewNamespace(ctx, "cm-ns", &parts.NamespaceArgs{
			Name: pulumi.String("cm-ns"),
			AdditionalLabels: pulumi.StringMap{
				"app.kubernetes.io/component": pulumi.String("mulval"),
				"app.kubernetes.io/part-of":   pulumi.String("mulval"),
				"cvewatcher/stack-name":       pulumi.String(ctx.Stack()),
			},
		}, opts...)
		if err != nil {
			return err
		}
		namespace = mv.ns.Name
	}

	// Deploy PostgreSQL
	mv.pgsql, err = parts.NewPostgreSQL(ctx, "backend", &parts.PostgreSQLArgs{
		DatabaseName:              pulumi.String("mulval-backend"),
		Namespace:                 namespace,
		Registry:                  args.Registry,
		PgToAPIServerTemplate:     args.PgToAPIServerTemplate,
		ClusterNamePrefix:         pulumi.String("mulval"),
		PostgresOperatorNamespace: args.PostgresOperatorNamespace,
		StorageClassName:          args.StorageClassName,
		Replicas:                  pulumi.Int(1), // No need for a big database
	}, opts...)
	if err != nil {
		return
	}

	// Deploy NATS
	mv.nats, err = parts.NewNats(ctx, "events", &parts.NatsArgs{
		Namespace:        namespace,
		Replicas:         pulumi.Int(1), // No need for a replicated stuff
		StorageClassName: args.StorageClassName,
	}, opts...)
	if err != nil {
		return
	}

	// Deploy MulVal
	mv.mv, err = parts.NewMulVal(ctx, "mulval", &parts.MulValArgs{
		Namespace: namespace,
		AdditionalLabels: pulumi.ToStringMap(map[string]string{
			"postgresql-client": "true", // For PostgreSQL Netpol label match
		}),
		Tag:            args.Tag,
		Registry:       args.Registry,
		LogLevel:       args.LogLevel,
		RomeoClaimName: args.RomeoClaimName,
		Requests:       args.Requests,
		Limits:         args.Limits,
		Swagger:        args.Swagger,
		UI:             args.UI,
		NatsEndpoint:   mv.nats.Endpoint,
		PgsqlEndpoint:  mv.pgsql.Endpoint,
		Otel:           args.OTel,
	}, opts...)
	if err != nil {
		return
	}

	// Netpol: MulVal -> PostgreSQL + MulVal -> NATS
	mv.mvToPgsqlAndNats, err = netwv1.NewNetworkPolicy(ctx, "mulval-to-pgsql-and-nats", &netwv1.NetworkPolicyArgs{
		Metadata: metav1.ObjectMetaArgs{
			Namespace: namespace,
			Labels: pulumi.StringMap{
				"app.kubernetes.io/component": pulumi.String("mulval"),
				"app.kubernetes.io/part-of":   pulumi.String("mulval"),
				"cvewatcher/stack-name":       pulumi.String(ctx.Stack()),
			},
		},
		Spec: netwv1.NetworkPolicySpecArgs{
			PodSelector: metav1.LabelSelectorArgs{
				MatchLabels: mv.mv.PodLabels,
			},
			PolicyTypes: pulumi.ToStringArray([]string{
				"Egress",
			}),
			Egress: netwv1.NetworkPolicyEgressRuleArray{
				// -> PostgreSQL
				netwv1.NetworkPolicyEgressRuleArgs{
					To: netwv1.NetworkPolicyPeerArray{
						netwv1.NetworkPolicyPeerArgs{
							NamespaceSelector: metav1.LabelSelectorArgs{
								MatchLabels: pulumi.StringMap{
									"kubernetes.io/metadata.name": namespace,
								},
							},
							PodSelector: metav1.LabelSelectorArgs{
								MatchLabels: mv.pgsql.PodLabels,
							},
						},
					},
					Ports: netwv1.NetworkPolicyPortArray{
						netwv1.NetworkPolicyPortArgs{
							Port:     parseEndpoint(mv.pgsql.Endpoint),
							Protocol: pulumi.String("TCP"),
						},
					},
				},
				// -> NATS
				netwv1.NetworkPolicyEgressRuleArgs{
					To: netwv1.NetworkPolicyPeerArray{
						netwv1.NetworkPolicyPeerArgs{
							NamespaceSelector: metav1.LabelSelectorArgs{
								MatchLabels: pulumi.StringMap{
									"kubernetes.io/metadata.name": namespace,
								},
							},
							PodSelector: metav1.LabelSelectorArgs{
								MatchLabels: mv.nats.PodLabels,
							},
						},
					},
					Ports: netwv1.NetworkPolicyPortArray{
						netwv1.NetworkPolicyPortArgs{
							Port:     parseEndpoint(mv.nats.Endpoint),
							Protocol: pulumi.String("TCP"),
						},
					},
				},
			},
		},
	}, opts...)
	if err != nil {
		return
	}

	// Netpol: NATS <- MulVal
	mv.natsFromMv, err = netwv1.NewNetworkPolicy(ctx, "nats-from-mulval", &netwv1.NetworkPolicyArgs{
		Metadata: metav1.ObjectMetaArgs{
			Namespace: namespace,
			Labels: pulumi.StringMap{
				"app.kubernetes.io/component": pulumi.String("mulval"),
				"app.kubernetes.io/part-of":   pulumi.String("mulval"),
				"cvewatcher/stack-name":       pulumi.String(ctx.Stack()),
			},
		},
		Spec: netwv1.NetworkPolicySpecArgs{
			PodSelector: metav1.LabelSelectorArgs{
				MatchLabels: mv.nats.PodLabels,
			},
			PolicyTypes: pulumi.ToStringArray([]string{
				"Ingress",
			}),
			Ingress: netwv1.NetworkPolicyIngressRuleArray{
				netwv1.NetworkPolicyIngressRuleArgs{
					From: netwv1.NetworkPolicyPeerArray{
						netwv1.NetworkPolicyPeerArgs{
							NamespaceSelector: metav1.LabelSelectorArgs{
								MatchLabels: pulumi.StringMap{
									"kubernetes.io/metadata.name": namespace,
								},
							},
							PodSelector: metav1.LabelSelectorArgs{
								MatchLabels: mv.mv.PodLabels,
							},
						},
					},
					Ports: netwv1.NetworkPolicyPortArray{
						netwv1.NetworkPolicyPortArgs{
							Port:     parseEndpoint(mv.nats.Endpoint),
							Protocol: pulumi.String("TCP"),
						},
					},
				},
			},
		},
	}, opts...)
	if err != nil {
		return
	}

	return
}

func (mv *MulVal) outputs(ctx *pulumi.Context) error {
	mv.PodLabels = mv.mv.PodLabels
	mv.Endpoint = mv.mv.Endpoint

	return ctx.RegisterResourceOutputs(mv, pulumi.Map{
		"podLabels": mv.PodLabels,
		"endpoint":  mv.Endpoint,
	})
}

// parseEndpoint cuts the input endpoint to return its port.
// Examples:
//   - some.thing:port -> port
//   - dns://some.thing:port -> port
func parseEndpoint(edp pulumi.StringInput) pulumi.IntOutput {
	return edp.ToStringOutput().ApplyT(func(edp string) (int, error) {
		// If it is a URL-formatted endpoint, parse it
		if u, err := url.Parse(edp); err == nil && u.Port() != "" {
			return parsePort(edp, u.Port())
		}

		// Else it should be a cuttable endpoint
		_, pStr, _ := strings.Cut(edp, ":")
		return parsePort(edp, pStr)
	}).(pulumi.IntOutput)
}

func parsePort(edp, port string) (int, error) {
	p, err := strconv.Atoi(port)
	if err != nil {
		return 0, errors.Wrapf(err, "parsing endpoint %s for port", edp)
	}
	return p, nil
}
