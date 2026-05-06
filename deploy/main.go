package main

import (
	"github.com/cvewatcher/mulval/deploy/common"
	"github.com/cvewatcher/mulval/deploy/services"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg, err := loadConfig(ctx)
		if err != nil {
			return err
		}

		var otelArgs *common.OTelArgs
		if cfg.OTel != nil {
			otelArgs = &common.OTelArgs{
				Endpoint: pulumi.String(cfg.OTel.Endpoint),
				Insecure: cfg.OTel.Insecure,
			}
		}
		mv, err := services.NewMulVal(ctx, "mulval", &services.MulValArgs{
			Namespace:                 pulumi.String(cfg.Namespace),
			Tag:                       pulumi.String(cfg.Tag),
			Registry:                  pulumi.String(cfg.Registry),
			LogLevel:                  pulumi.String(cfg.LogLevel),
			PVCAccessModes:            pulumi.ToStringArray(cfg.PVCAccessModes),
			PVCStorageSize:            pulumi.String(cfg.PVCStorageSize),
			PgToAPIServerTemplate:     pulumi.String(cfg.PgToAPIServerTemplate),
			PostgresOperatorNamespace: pulumi.String(cfg.PostgresOperatorNamespace),
			StorageClassName:          pulumi.String(cfg.StorageClassName),
			RomeoClaimName:            pulumi.String(cfg.RomeoClaimName),
			Requests:                  pulumi.ToStringMap(cfg.Requests),
			Limits:                    pulumi.ToStringMap(cfg.Limits),
			Swagger:                   cfg.Swagger,
			UI:                        cfg.UI,
			OTel:                      otelArgs,
		})
		if err != nil {
			return err
		}
		_ = mv

		ctx.Export("port", pulumi.Int(30000))
		return nil
	})
}

type (
	// Config holds the values configured using pulumi CLI.
	Config struct {
		Namespace                 string
		Tag                       string
		Registry                  string
		LogLevel                  string
		PVCAccessModes            []string
		PVCStorageSize            string
		PgToAPIServerTemplate     string
		PostgresOperatorNamespace string
		StorageClassName          string
		RomeoClaimName            string
		Requests, Limits          map[string]string
		Swagger, UI               bool

		OTel *OTelConfig
	}
	OTelConfig struct {
		Endpoint string `json:"endpoint"`
		Insecure bool   `json:"insecure"`
	}
)

func loadConfig(ctx *pulumi.Context) (*Config, error) {
	cfg := config.New(ctx, "")
	c := &Config{
		Namespace:                 cfg.Get("namespace"),
		Tag:                       cfg.Get("tag"),
		Registry:                  cfg.Get("registry"),
		LogLevel:                  cfg.Get("log-level"),
		PVCAccessModes:            []string{},
		PVCStorageSize:            cfg.Get("pvc-storage-size"),
		PgToAPIServerTemplate:     cfg.Get("pg-to-api-server-template"),
		PostgresOperatorNamespace: cfg.Get("postgres-operator-namespace"),
		StorageClassName:          cfg.Get("storage-class-name"),
		RomeoClaimName:            cfg.Get("romeo-claim-name"),
		Requests:                  map[string]string{},
		Limits:                    map[string]string{},
		Swagger:                   cfg.GetBool("swagger"),
		UI:                        cfg.GetBool("ui"),
		OTel:                      nil, // provided later
	}

	// As we cannot default this one, we silently drop the error is not are set
	_ = cfg.TryObject("pvc-access-modes", &c.PVCAccessModes)
	_ = cfg.TryObject("requests", &c.Requests)
	_ = cfg.TryObject("limits", &c.Limits)

	var otelC OTelConfig
	if err := cfg.TryObject("otel", &otelC); err == nil && otelC.Endpoint != "" {
		c.OTel = &otelC
	}

	return c, nil
}
