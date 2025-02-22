package observability

import (
	"context"
	"errors"
	"fmt"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/metric"
	exportmetric "go.opentelemetry.io/otel/sdk/export/metric"
	"go.opentelemetry.io/otel/sdk/export/metric/aggregation"
	controller "go.opentelemetry.io/otel/sdk/metric/controller/basic"
	otelprocessor "go.opentelemetry.io/otel/sdk/metric/processor/basic"
	selector "go.opentelemetry.io/otel/sdk/metric/selector/simple"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.7.0"
	"go.uber.org/zap"
	"net/http"
	"os"
	"time"
)

const (
	clusterIdEnv              = "CLUSTER_ID"
	userIdEnv                 = "USER_ID"
	regionIdEnv               = "REGION_ID"
	KindlingServiceNamePrefix = "kindling"
	StdoutKindExporter        = "stdout"
	OtlpGrpcKindExporter      = "otlp"
	PrometheusKindExporter    = "prometheus"
)

type otelLoggerHandler struct {
	logger *zap.Logger
}

func (h *otelLoggerHandler) Handle(err error) {
	h.logger.Warn("Opentelemetry-go encountered an error: ", zap.Error(err))
}

func InitTelemetry(logger *zap.Logger, config *Config) (metric.MeterProvider, error) {
	otel.SetErrorHandler(&otelLoggerHandler{logger: logger})
	hostName, err := os.Hostname()
	if err != nil {
		logger.Info("Cannot get hostname; set hostname unknown: ", zap.Error(err))
		hostName = "unknown"
	}

	clusterId, ok := os.LookupEnv(clusterIdEnv)
	if !ok {
		logger.Info("[CLUSTER_ID] is not found in env variable which will be set [noclusteridset]")
		clusterId = "noclusteridset"
	}

	serviceName := KindlingServiceNamePrefix + "-" + clusterId
	rs, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
			semconv.ServiceInstanceIDKey.String(hostName),
		),
	)

	if config.ExportKind == PrometheusKindExporter {
		// Create controller
		c := controller.New(
			otelprocessor.NewFactory(
				selector.NewWithInexpensiveDistribution(),
				aggregation.CumulativeTemporalitySelector(),
			),
			controller.WithResource(rs),
		)

		promConfig := prometheus.Config{
			DefaultHistogramBoundaries: exponentialInt64NanosecondsBoundaries,
		}
		exp, err := prometheus.New(promConfig, c)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize self-telemetry prometheus %w", err)
		}

		go func() {
			err := StartServer(exp, logger, config.PromCfg.Port)
			if err != nil {
				logger.Warn("error starting self-telemetry server: ", zap.Error(err))
			}
		}()
		return exp.MeterProvider(), nil
	} else {
		var collectPeriod time.Duration

		if config.ExportKind == StdoutKindExporter {
			collectPeriod = config.StdoutCfg.CollectPeriod
		} else if config.ExportKind == OtlpGrpcKindExporter {
			collectPeriod = config.OtlpGrpcCfg.CollectPeriod
		} else {
			return nil, fmt.Errorf("no self-telemetry exporter kind matched, current: [%v]", config.ExportKind)
		}

		exporters, err := newExporters(context.Background(), config, logger)
		if err != nil {
			return nil, fmt.Errorf("error happened when creating self-telemetry exporter: %w", err)
		}

		cont := controller.New(
			otelprocessor.NewFactory(selector.NewWithInexpensiveDistribution(), exporters),
			controller.WithExporter(exporters),
			controller.WithCollectPeriod(collectPeriod),
			controller.WithResource(rs),
		)

		if err = cont.Start(context.Background()); err != nil {
			return nil, fmt.Errorf("failed to start self-telemetry controller: %w", err)
		}

		return cont, nil
	}
}

// Crete new opentelemetry-go exporter.
func newExporters(context context.Context, cfg *Config, logger *zap.Logger) (exportmetric.Exporter, error) {
	logger.Sugar().Infof("Initializing self-observability exporter whose type is %s", cfg.ExportKind)
	switch cfg.ExportKind {
	case StdoutKindExporter:
		metricExp, err := stdoutmetric.New()
		if err != nil {
			return nil, fmt.Errorf("failed to create exporter, %w", err)
		}
		return metricExp, nil
	case OtlpGrpcKindExporter:
		metricExporter, err := otlpmetricgrpc.New(context,
			otlpmetricgrpc.WithInsecure(),
			otlpmetricgrpc.WithEndpoint(cfg.OtlpGrpcCfg.Endpoint),
			otlpmetricgrpc.WithRetry(otlpmetricgrpc.RetrySettings{
				Enabled:         true,
				InitialInterval: 300 * time.Millisecond,
				MaxInterval:     5 * time.Second,
				MaxElapsedTime:  15 * time.Second,
			}),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create exporter, %w", err)
		}
		return metricExporter, nil
	default:
		return nil, errors.New("failed to create exporter, no exporter kind is provided")
	}
}

func StartServer(exporter *prometheus.Exporter, logger *zap.Logger, port string) error {
	serveMux := http.ServeMux{}
	serveMux.HandleFunc("/metrics", exporter.ServeHTTP)

	srv := http.Server{
		Addr:    port,
		Handler: &serveMux,
	}

	logger.Sugar().Infof("Prometheus Server listening at port: [%s]", port)
	err := srv.ListenAndServe()

	if err != nil && err != http.ErrServerClosed {
		return err
	}

	logger.Sugar().Infof("Prometheus gracefully shutdown the http server...\n")

	return nil
}
