package main

import (
	"strconv"
	"strings"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	netwv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/networking/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"

	"github.com/cvewatcher/mulval/deploy/services"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg := loadConfig(ctx)

		mulval, err := services.NewMulVal(ctx, "integration-tests", &services.MulValArgs{
			Namespace: pulumi.String(cfg.Namespace),
			Tag:       pulumi.String(cfg.Tag),
			Registry:  pulumi.String(cfg.Registry),
			LogLevel:  pulumi.String("info"),
			PVCAccessModes: pulumi.ToStringArray([]string{
				"ReadWriteOnce", // don't need to scale (+ not possible with kind in CI)
			}),
			PVCStorageSize:            pulumi.String("2Gi"),
			PgToAPIServerTemplate:     nil, // default to Cilium CNI so already matching
			PostgresOperatorNamespace: pulumi.String("cnpg-system"),
			StorageClassName:          pulumi.String(""), // will default
			RomeoClaimName:            pulumi.String(cfg.RomeoClaimName),
		})
		if err != nil {
			return err
		}

		svc, err := corev1.NewService(ctx, "expose-mulval", &corev1.ServiceArgs{
			Metadata: metav1.ObjectMetaArgs{
				Namespace: pulumi.String(cfg.Namespace),
				Labels: pulumi.StringMap{
					"app.kubernetes.io/component": pulumi.String("mulval"),
					"app.kubernetes.io/part-of":   pulumi.String("mulval"),
					"cvewatcher/stack-name":       pulumi.String(ctx.Stack()),
				},
			},
			Spec: corev1.ServiceSpecArgs{
				Type:     pulumi.String("NodePort"),
				Selector: mulval.PodLabels,
				Ports: corev1.ServicePortArray{
					corev1.ServicePortArgs{
						Port: mulval.Endpoint.ApplyT(func(edp string) int {
							pts := strings.Split(edp, ":")
							p := pts[len(pts)-1]
							port, _ := strconv.Atoi(p)
							return port
						}).(pulumi.IntOutput),
					},
				},
			},
		})
		if err != nil {
			return err
		}

		if _, err := netwv1.NewNetworkPolicy(ctx, "expose-mulval", &netwv1.NetworkPolicyArgs{
			Metadata: metav1.ObjectMetaArgs{
				Namespace: pulumi.String(cfg.Namespace),
				Labels: pulumi.StringMap{
					"app.kubernetes.io/component": pulumi.String("mulval"),
					"app.kubernetes.io/part-of":   pulumi.String("mulval"),
					"cvewatcher/stack-name":       pulumi.String(ctx.Stack()),
				},
			},
			Spec: netwv1.NetworkPolicySpecArgs{
				PodSelector: metav1.LabelSelectorArgs{
					MatchLabels: mulval.PodLabels,
				},
				PolicyTypes: pulumi.ToStringArray([]string{
					"Ingress",
				}),
				Ingress: netwv1.NetworkPolicyIngressRuleArray{
					netwv1.NetworkPolicyIngressRuleArgs{
						From: netwv1.NetworkPolicyPeerArray{
							netwv1.NetworkPolicyPeerArgs{
								IpBlock: netwv1.IPBlockArgs{
									Cidr: pulumi.String("0.0.0.0/0"),
								},
							},
						},
						Ports: netwv1.NetworkPolicyPortArray{
							netwv1.NetworkPolicyPortArgs{
								Port: mulval.Endpoint.ApplyT(func(edp string) int {
									pts := strings.Split(edp, ":")
									p := pts[len(pts)-1]
									port, _ := strconv.Atoi(p)
									return port
								}).(pulumi.IntOutput),
							},
						},
					},
				},
			},
		}); err != nil {
			return err
		}

		ctx.Export("exposed_port", svc.Spec.Ports().Index(pulumi.Int(0)).NodePort().Elem())
		return nil
	})
}

type (
	// Config holds the values configured using pulumi CLI.
	Config struct {
		Namespace      string
		Tag            string
		Registry       string
		RomeoClaimName string
	}
)

func loadConfig(ctx *pulumi.Context) *Config {
	cfg := config.New(ctx, "")
	return &Config{
		Namespace:      cfg.Get("namespace"),
		Tag:            cfg.Get("tag"),
		Registry:       cfg.Get("registry"),
		RomeoClaimName: cfg.Get("romeo-claim-name"),
	}
}
