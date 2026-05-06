package parts

import (
	"sync"

	helmv4 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/helm/v4"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	netwv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/networking/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type (
	Nats struct {
		pulumi.ResourceState

		chart      *helmv4.Chart
		clusterPol *netwv1.NetworkPolicy

		Endpoint  pulumi.StringOutput
		PodLabels pulumi.StringMapOutput
	}

	NatsArgs struct {
		Namespace pulumi.StringInput

		Replicas pulumi.IntInput
		replicas pulumi.IntOutput
		cluster  bool

		StorageClassName pulumi.StringInput
	}
)

func NewNats(ctx *pulumi.Context, name string, args *NatsArgs, opts ...pulumi.ResourceOption) (*Nats, error) {
	nats := &Nats{}
	args = nats.defaults(args)

	if err := ctx.RegisterComponentResource("cvewatcher:mulval:nats", name, nats, opts...); err != nil {
		return nil, err
	}
	opts = append(opts, pulumi.Parent(nats))
	if err := nats.provision(ctx, args, opts...); err != nil {
		return nil, err
	}
	if err := nats.outputs(ctx); err != nil {
		return nil, err
	}
	return nats, nil
}

func (nats *Nats) defaults(args *NatsArgs) *NatsArgs {
	if args == nil {
		args = &NatsArgs{}
	}
	wg := sync.WaitGroup{}

	// Replicas must be at least 1
	args.replicas = pulumi.Int(1).ToIntOutput()
	if args.Replicas != nil {
		wg.Add(1)
		args.replicas = args.Replicas.ToIntOutput().ApplyT(func(replicas int) int {
			defer wg.Done()

			if replicas < 1 {
				replicas = 1
			}

			args.cluster = replicas > 1

			return replicas
		}).(pulumi.IntOutput)
	}

	wg.Wait()
	return args
}

// Inspired from https://oneuptime.com/blog/post/2026-02-02-nats-kubernetes/view, but most of the
// config seem to come from some AI slop as the names were slightly off...
func (nats *Nats) provision(ctx *pulumi.Context, args *NatsArgs, opts ...pulumi.ResourceOption) (err error) {
	nats.PodLabels = pulumi.StringMap{
		"app.kubernetes.io/component": pulumi.String("nats"),
		"app.kubernetes.io/part-of":   pulumi.String("mulval"),
		"cvewatcher/stack-name":       pulumi.String(ctx.Stack()),
	}.ToStringMapOutput()

	nats.chart, err = helmv4.NewChart(ctx, "nats-chart", &helmv4.ChartArgs{
		RepositoryOpts: helmv4.RepositoryOptsArgs{
			Repo: pulumi.String("https://nats-io.github.io/k8s/helm/charts/"),
		},
		Chart:     pulumi.String("nats"),
		Namespace: args.Namespace,
		Values: pulumi.Map{
			"global": pulumi.Map{
				"labels": nats.PodLabels,
			},
			"config": pulumi.Map{
				"cluster": pulumi.Map{
					"enabled":  pulumi.Bool(args.cluster),
					"replicas": args.replicas,
				},
				"jetstream": pulumi.Map{
					"enabled": pulumi.Bool(true), // We need JetStream per the WorkGroups
					"memoryStore": pulumi.Map{
						"enabled": pulumi.Bool(true),
						"size":    pulumi.String("2Gi"),
					},
					"fileStore": pulumi.Map{
						"enabled":          pulumi.Bool(true),
						"size":             pulumi.String("10Gi"),
						"storageClassName": args.StorageClassName,
					},
				},
				"monitor": pulumi.Map{
					// Do not expose Prometheus monitoring, we don't scrap these
					"enabled": pulumi.Bool(false),
				},
			},
			"container": pulumi.Map{
				"resources": pulumi.Map{
					"requests": pulumi.Map{
						"cpu":    pulumi.String("100m"),  // minimum CPU guaranteed
						"memory": pulumi.String("256Mi"), // minimum memory guaranteed
					},
					"limits": pulumi.Map{
						"cpu":    pulumi.String("500m"), // maximum CPU allowed
						"memory": pulumi.String("1Gi"),  // maximum memory allowed
					},
				},
			},
			"natsBox": pulumi.Map{
				// Disable nats-box, don't need it for prod.
				// For dev purposes, forward port or manually enable it).
				"enabled": pulumi.Bool(false),
			},
			"reloader": pulumi.Map{
				// Do not live-reload the configuration, block lateral movements if access to filesystem
				"enabled": pulumi.Bool(false),
			},
			"service": pulumi.Map{
				"enabled": pulumi.Bool(false), // We already have the headless service, don't need this one too
			},
		},
	}, opts...)
	if err != nil {
		return
	}

	if args.cluster {
		nats.clusterPol, err = netwv1.NewNetworkPolicy(ctx, "nats-clustering", &netwv1.NetworkPolicyArgs{
			Metadata: metav1.ObjectMetaArgs{
				Namespace: args.Namespace,
			},
			Spec: netwv1.NetworkPolicySpecArgs{
				PolicyTypes: pulumi.ToStringArray([]string{
					"Ingress",
					"Egress",
				}),
				PodSelector: metav1.LabelSelectorArgs{
					MatchLabels: nats.PodLabels,
				},
				Ingress: netwv1.NetworkPolicyIngressRuleArray{
					netwv1.NetworkPolicyIngressRuleArgs{
						From: netwv1.NetworkPolicyPeerArray{
							netwv1.NetworkPolicyPeerArgs{
								NamespaceSelector: metav1.LabelSelectorArgs{
									MatchLabels: pulumi.StringMap{
										"kubernetes.io/metadata.name": args.Namespace,
									},
								},
								PodSelector: metav1.LabelSelectorArgs{
									MatchLabels: nats.PodLabels,
								},
							},
						},
					},
				},
				Egress: netwv1.NetworkPolicyEgressRuleArray{
					netwv1.NetworkPolicyEgressRuleArgs{
						To: netwv1.NetworkPolicyPeerArray{
							netwv1.NetworkPolicyPeerArgs{
								NamespaceSelector: metav1.LabelSelectorArgs{
									MatchLabels: pulumi.StringMap{
										"kubernetes.io/metadata.name": args.Namespace,
									},
								},
								PodSelector: metav1.LabelSelectorArgs{
									MatchLabels: nats.PodLabels,
								},
							},
						},
						Ports: netwv1.NetworkPolicyPortArray{
							netwv1.NetworkPolicyPortArgs{
								Port: pulumi.String("cluster"),
							},
						},
					},
				},
			},
		}, opts...)
		if err != nil {
			return err
		}
	}

	return
}

func (nats *Nats) outputs(ctx *pulumi.Context) error {
	// TODO use the outcomes of the chart to determine the service name and port
	nats.Endpoint = pulumi.String("nats://nats-chart-headless:4222").ToStringOutput()

	return ctx.RegisterResourceOutputs(nats, pulumi.Map{
		"podLabels": nats.PodLabels,
		"endpoint":  nats.Endpoint,
	})
}
