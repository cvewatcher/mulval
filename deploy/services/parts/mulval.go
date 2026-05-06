package parts

import (
	"strings"
	"sync"

	"github.com/cvewatcher/mulval/deploy/common"
	"github.com/cvewatcher/mulval/pkg/config"
	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"gopkg.in/yaml.v3"
)

type (
	MulVal struct {
		pulumi.ResourceState

		cm  *corev1.ConfigMap
		dep *appsv1.Deployment
		svc *corev1.Service

		PodLabels pulumi.StringMapOutput
		Endpoint  pulumi.StringOutput
	}

	MulValArgs struct {
		// Namespace to which deploy the MulVal resources.
		// It is different from the namespace the MulVal will deploy instances to,
		// which will be created on the fly.
		Namespace pulumi.StringInput

		// AdditionalLabels to pass to the namespace, mostly for filtering purposes.
		AdditionalLabels pulumi.StringMapInput

		// Tag defines the specific tag to run MulVal to.
		// If not specified, defaults to "latest".
		Tag pulumi.StringPtrInput
		tag pulumi.StringOutput

		// Registry define from where to fetch the MulVal Docker images.
		// If set empty, defaults to Docker Hub.
		// Authentication is not supported, please provide it as Kubernetes-level configuration.
		Registry pulumi.StringPtrInput
		registry pulumi.StringOutput

		// LogLevel defines the level at which to log.
		LogLevel pulumi.StringInput
		logLevel pulumi.StringOutput

		// RomeoClaimName, if set, will turn on the coverage export of MulVal for later download.
		RomeoClaimName pulumi.StringInput
		mountCoverdir  bool

		// Requests for the MulVal container. For more infos:
		// https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/
		Requests pulumi.StringMapInput
		requests pulumi.StringMapOutput

		// Limits for the MulVal container. For more infos:
		// https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/
		Limits pulumi.StringMapInput
		limits pulumi.StringMapOutput

		Swagger, UI bool

		NatsEndpoint  pulumi.StringInput
		PgsqlEndpoint pulumi.StringInput
		Otel          *common.OTelArgs
	}
)

const (
	port     = 8080
	coverdir = "/etc/coverdir"

	defaultTag      = "latest"
	defaultLogLevel = "info"
)

func NewMulVal(ctx *pulumi.Context, name string, args *MulValArgs, opts ...pulumi.ResourceOption) (*MulVal, error) {
	mv := &MulVal{}

	args = mv.defaults(args)
	if err := ctx.RegisterComponentResource("cvewatcher:mulval:mulval-as-a-service", name, mv, opts...); err != nil {
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

	if args.AdditionalLabels == nil {
		args.AdditionalLabels = pulumi.StringMap{}.ToStringMapOutput()
	}

	args.tag = pulumi.String(defaultTag).ToStringOutput()
	if args.Tag != nil {
		args.tag = args.Tag.ToStringPtrOutput().ApplyT(func(tag *string) string {
			if tag == nil || *tag == "" {
				return defaultTag
			}
			return *tag
		}).(pulumi.StringOutput)
	}

	args.registry = pulumi.String("").ToStringOutput()
	if args.Registry != nil {
		args.registry = args.Registry.ToStringPtrOutput().ApplyT(func(in *string) string {
			// No private registry -> defaults to Docker Hub
			if in == nil {
				return ""
			}

			str := *in
			// If one set, make sure it ends with one '/'
			if str != "" && !strings.HasSuffix(str, "/") {
				str = str + "/"
			}
			return str
		}).(pulumi.StringOutput)
	}

	args.logLevel = pulumi.String(defaultLogLevel).ToStringOutput()
	if args.LogLevel != nil {
		args.logLevel = args.LogLevel.ToStringOutput()
	}

	if args.RomeoClaimName != nil {
		wg := &sync.WaitGroup{}
		wg.Add(1)
		args.RomeoClaimName.ToStringOutput().ApplyT(func(rcn string) error {
			args.mountCoverdir = rcn != ""
			wg.Done()
			return nil
		})
		wg.Wait()
	}

	args.requests = pulumi.StringMap{}.ToStringMapOutput()
	if args.Requests != nil {
		args.requests = args.Requests.ToStringMapOutput()
	}

	args.limits = pulumi.StringMap{}.ToStringMapOutput()
	if args.Limits != nil {
		args.limits = args.Limits.ToStringMapOutput()
	}

	return args
}

func (mv *MulVal) provision(ctx *pulumi.Context, args *MulValArgs, opts ...pulumi.ResourceOption) (err error) {
	mv.cm, err = corev1.NewConfigMap(ctx, "config", &corev1.ConfigMapArgs{
		Metadata: metav1.ObjectMetaArgs{
			Namespace: args.Namespace,
			Labels: pulumi.StringMap{
				"app.kubernetes.io/name":      pulumi.String("mulval"),
				"app.kubernetes.io/version":   args.tag,
				"app.kubernetes.io/component": pulumi.String("mulval"),
				"app.kubernetes.io/part-of":   pulumi.String("mulval"),
				"cvewatcher/stack-name":       pulumi.String(ctx.Stack()),
			},
		},
		Data: pulumi.StringMap{
			"config.yaml": pulumi.All(args.LogLevel, args.NatsEndpoint, args.PgsqlEndpoint).ApplyT(func(all []any) string {
				conf := config.New()
				ll := all[0].(string)
				conf.LogLevel = config.LogLevel(ll)

				conf.Events.URL = all[1].(string)
				conf.Events.InstanceID = &config.FromEnv{Content: "mulval"}

				conf.Storage.DSN = all[2].(string)
				conf.Storage.Migrate = true
				conf.Storage.MinConns = 4

				b, _ := yaml.Marshal(conf)
				return string(b)
			}).(pulumi.StringOutput),
		},
	}, opts...)
	if err != nil {
		return
	}

	envs := corev1.EnvVarArray{
		corev1.EnvVarArgs{
			Name:  pulumi.String("PORT"),
			Value: pulumi.Sprintf("%d", port),
		},
		corev1.EnvVarArgs{
			Name:  pulumi.String("SWAGGER"),
			Value: pulumi.Sprintf("%t", args.Swagger),
		},
		corev1.EnvVarArgs{
			Name:  pulumi.String("UI"),
			Value: pulumi.Sprintf("%t", args.UI),
		},
		corev1.EnvVarArgs{
			Name:  pulumi.String("CONFIG"),
			Value: pulumi.String("/config.yaml"),
		},
	}
	if args.Otel != nil {
		envs = append(envs,
			corev1.EnvVarArgs{
				Name: pulumi.String("OTEL_SERVICE_NAME"),
				Value: args.Otel.ServiceName.ToStringPtrOutput().ApplyT(func(sn *string) string {
					if sn == nil || *sn == "" {
						return "mulval"
					}
					return *sn
				}).(pulumi.StringOutput),
			},
			corev1.EnvVarArgs{
				Name: pulumi.String("OTEL_EXPORTER_OTLP_ENDPOINT"),
				Value: args.Otel.Endpoint.ToStringOutput().ApplyT(func(edp string) string {
					beginWithDNS := strings.HasPrefix(edp, "dns://")     // the basic OTEL scheme to use
					beginWithHTTP := strings.HasPrefix(edp, "http://")   // then a gateway
					beginWithHTTPS := strings.HasPrefix(edp, "https://") // and a secured gateway

					if !(beginWithDNS || beginWithHTTP || beginWithHTTPS) {
						edp = "dns://" + edp
					}
					return edp
				}).(pulumi.StringOutput),
			},
			corev1.EnvVarArgs{
				Name:  pulumi.String("OTEL_EXPORTER_OTLP_PROTOCOL"),
				Value: pulumi.String("grpc"), // XXX big assumption here
			},
		)
		if args.Otel.Insecure {
			envs = append(envs,
				corev1.EnvVarArgs{
					Name:  pulumi.String("OTEL_EXPORTER_OTLP_INSECURE"),
					Value: pulumi.String("true"),
				},
			)
		}
	}

	if args.mountCoverdir {
		envs = append(envs, corev1.EnvVarArgs{
			Name:  pulumi.String("GOCOVERDIR"),
			Value: pulumi.String(coverdir),
		})
	}

	// => Deployment
	mv.PodLabels = pulumi.All(args.AdditionalLabels, args.tag).ApplyT(func(all []any) map[string]string {
		out := all[0].(map[string]string)
		out["app.kubernetes.io/name"] = "mulval"
		out["app.kubernetes.io/version"] = all[1].(string)
		out["app.kubernetes.io/component"] = "mulval"
		out["app.kubernetes.io/part-of"] = "mulval"
		out["app.kubernetes.io/part-of"] = ctx.Stack()
		return out
	}).(pulumi.StringMapOutput)
	mv.dep, err = appsv1.NewDeployment(ctx, "mulval-deployment", &appsv1.DeploymentArgs{
		Metadata: metav1.ObjectMetaArgs{
			Namespace: args.Namespace,
			Labels:    mv.PodLabels,
		},
		Spec: appsv1.DeploymentSpecArgs{
			Replicas: pulumi.Int(1), // MulVal (as a Service) cannot scale
			Selector: metav1.LabelSelectorArgs{
				MatchLabels: mv.PodLabels,
			},
			Template: corev1.PodTemplateSpecArgs{
				Metadata: metav1.ObjectMetaArgs{
					Namespace: args.Namespace,
					Labels:    mv.PodLabels,
				},
				Spec: corev1.PodSpecArgs{
					Containers: corev1.ContainerArray{
						corev1.ContainerArgs{
							Name:            pulumi.String("mulval"),
							Image:           pulumi.Sprintf("%scvewatcher/mulval:%s", args.registry, args.tag),
							ImagePullPolicy: pulumi.String("Always"),
							Env:             envs,
							Ports: corev1.ContainerPortArray{
								corev1.ContainerPortArgs{
									Name:          pulumi.String("api"),
									ContainerPort: pulumi.Int(port),
								},
							},
							VolumeMounts: func() corev1.VolumeMountArrayOutput {
								vms := corev1.VolumeMountArray{
									corev1.VolumeMountArgs{
										Name:      pulumi.String("config"),
										MountPath: pulumi.String("/config.yaml"),
										SubPath:   pulumi.String("config.yaml"),
										ReadOnly:  pulumi.BoolPtr(true), // Injected files should not be mutated
									},
								}
								if args.mountCoverdir {
									vms = append(vms, corev1.VolumeMountArgs{
										Name:      pulumi.String("coverdir"),
										MountPath: pulumi.String(coverdir),
									})
								}
								return vms.ToVolumeMountArrayOutput()
							}(),
							ReadinessProbe: corev1.ProbeArgs{
								HttpGet: corev1.HTTPGetActionArgs{
									Path: pulumi.String("/healthcheck"),
									Port: pulumi.Int(port),
								},
							},
							Resources: corev1.ResourceRequirementsArgs{
								Requests: args.requests,
								Limits:   args.limits,
							},
						},
					},
					Volumes: func() corev1.VolumeArrayOutput {
						vs := corev1.VolumeArray{
							corev1.VolumeArgs{
								Name: pulumi.String("config"),
								ConfigMap: corev1.ConfigMapVolumeSourceArgs{
									Name:        mv.cm.Metadata.Name().Elem(),
									DefaultMode: pulumi.Int(0444), // -r--r--r--
									Items: corev1.KeyToPathArray{
										corev1.KeyToPathArgs{
											Key:  pulumi.String("config.yaml"),
											Path: pulumi.String("config.yaml"),
										},
									},
								},
							},
						}
						if args.mountCoverdir {
							vs = append(vs, corev1.VolumeArgs{
								Name: pulumi.String("coverdir"),
								PersistentVolumeClaim: corev1.PersistentVolumeClaimVolumeSourceArgs{
									ClaimName: args.RomeoClaimName,
								},
							})
						}
						return vs.ToVolumeArrayOutput()
					}(),
				},
			},
		},
	}, opts...)
	if err != nil {
		return
	}

	// => Service
	mv.svc, err = corev1.NewService(ctx, "mulval-service", &corev1.ServiceArgs{
		Metadata: metav1.ObjectMetaArgs{
			Namespace: args.Namespace,
			Labels: pulumi.StringMap{
				"app.kubernetes.io/component": pulumi.String("mulval"),
				"app.kubernetes.io/part-of":   pulumi.String("mulval"),
				"cvewatcher/stack-name":       pulumi.String(ctx.Stack()),
			},
		},
		Spec: corev1.ServiceSpecArgs{
			ClusterIP: pulumi.String("None"), // Headless, for DNS purposes
			Ports: corev1.ServicePortArray{
				corev1.ServicePortArgs{
					Name: pulumi.String("api"),
					Port: pulumi.Int(port),
				},
			},
			Selector: mv.dep.Spec.Template().Metadata().Labels(),
		},
	}, opts...)
	if err != nil {
		return
	}

	return
}

func (mv *MulVal) outputs(ctx *pulumi.Context) error {
	// mv.PodLabels is defined during provisionning such that it can be returned for
	// netpols. Then, they can be created to grant network traffic (mv->pgsql + mv->nats)
	// necessary for the readiness probe.

	mv.Endpoint = pulumi.Sprintf("%s.%s:%d", mv.svc.Metadata.Name().Elem(), mv.svc.Metadata.Namespace().Elem(), port)

	return ctx.RegisterResourceOutputs(mv, pulumi.Map{
		"podLabels": mv.PodLabels,
		"endpoint":  mv.Endpoint,
	})
}
