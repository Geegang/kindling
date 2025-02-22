package application

import (
	"flag"
	"fmt"
	"github.com/Kindling-project/kindling/collector/analyzer"
	"github.com/Kindling-project/kindling/collector/analyzer/loganalyzer"
	"github.com/Kindling-project/kindling/collector/analyzer/network"
	"github.com/Kindling-project/kindling/collector/analyzer/tcpmetricanalyzer"
	"github.com/Kindling-project/kindling/collector/component"
	"github.com/Kindling-project/kindling/collector/consumer"
	"github.com/Kindling-project/kindling/collector/consumer/exporter/logexporter"
	"github.com/Kindling-project/kindling/collector/consumer/exporter/otelexporter"
	"github.com/Kindling-project/kindling/collector/consumer/processor/aggregateprocessor"
	"github.com/Kindling-project/kindling/collector/consumer/processor/k8sprocessor"
	"github.com/Kindling-project/kindling/collector/receiver"
	"github.com/Kindling-project/kindling/collector/receiver/udsreceiver"
	"github.com/spf13/viper"
	"go.uber.org/multierr"
)

type Application struct {
	viper             *viper.Viper
	componentsFactory *ComponentsFactory
	telemetry         *component.TelemetryManager
	receiver          receiver.Receiver
	analyzerManager   analyzer.Manager
}

func New() (*Application, error) {
	app := &Application{
		viper:             viper.New(),
		componentsFactory: NewComponentsFactory(),
		telemetry:         component.NewTelemetryManager(),
	}
	app.registerFactory()
	// Initialize flags
	configPath := flag.String("config", "kindling-collector-config.yml", "Configuration file")
	flag.Parse()
	err := app.readInConfig(*configPath)
	if err != nil {
		return nil, fmt.Errorf("fail to read configuration: %w", err)
	}
	// Build processing pipeline
	err = app.buildPipeline()
	if err != nil {
		return nil, fmt.Errorf("failed to build pipeline: %w", err)
	}
	return app, nil
}

func (a *Application) Run() error {
	err := a.analyzerManager.StartAll(a.telemetry.Telemetry.Logger)
	if err != nil {
		return fmt.Errorf("failed to start application: %v", err)
	}
	// Wait until the receiver shutdowns
	err = a.receiver.Start()
	if err != nil {
		return fmt.Errorf("failed to start application: %v", err)
	}
	return nil
}

func (a *Application) Shutdown() error {
	return multierr.Combine(a.receiver.Shutdown(), a.analyzerManager.ShutdownAll(a.telemetry.Telemetry.Logger))
}

func initFlags() error {
	return nil
}

func (a *Application) registerFactory() {
	a.componentsFactory.RegisterReceiver(udsreceiver.Uds, udsreceiver.NewUdsReceiver, &udsreceiver.Config{})
	a.componentsFactory.RegisterAnalyzer(network.Network.String(), network.NewNetworkAnalyzer, &network.Config{})
	a.componentsFactory.RegisterProcessor(k8sprocessor.K8sMetadata, k8sprocessor.NewKubernetesProcessor, &k8sprocessor.DefaultConfig)
	//a.componentsFactory.RegisterProcessor(kindlingformatprocessor.ProcessorName, kindlingformatprocessor.NewRelabelProcessor, &kindlingformatprocessor.Config{})
	a.componentsFactory.RegisterExporter(otelexporter.Otel, otelexporter.NewExporter, &otelexporter.Config{})
	a.componentsFactory.RegisterAnalyzer(tcpmetricanalyzer.TcpMetric.String(), tcpmetricanalyzer.NewTcpMetricAnalyzer, &tcpmetricanalyzer.Config{})
	a.componentsFactory.RegisterExporter(logexporter.Type, logexporter.New, &logexporter.Config{})
	a.componentsFactory.RegisterAnalyzer(loganalyzer.Type.String(), loganalyzer.New, &loganalyzer.Config{})
	a.componentsFactory.RegisterProcessor(aggregateprocessor.Type, aggregateprocessor.New, aggregateprocessor.NewDefaultConfig())
}

func (a *Application) readInConfig(path string) error {
	a.viper.SetConfigFile(path)
	err := a.viper.ReadInConfig()
	if err != nil { // Handle errors reading the config file
		return fmt.Errorf("error happened while reading config file: %w", err)
	}
	a.telemetry.ConstructConfig(a.viper)
	err = a.componentsFactory.ConstructConfig(a.viper)
	if err != nil {
		return fmt.Errorf("error happened while constructing config: %w", err)
	}
	return nil
}

// buildPipeline builds a event processing pipeline based on hard-code.
func (a *Application) buildPipeline() error {
	// TODO: Build pipeline via configuration to implement dependency injection
	// Initialize exporters
	otelExporterFactory := a.componentsFactory.Exporters[otelexporter.Otel]
	otelExporter := otelExporterFactory.NewFunc(otelExporterFactory.Config, a.telemetry.Telemetry)
	// Initialize all processors
	// 1. Kindling Metric Format Processor
	//formatProcessorFactory := a.componentsFactory.Processors[kindlingformatprocessor.ProcessorName]
	//formatProcessor := formatProcessorFactory.NewFunc(formatProcessorFactory.Config, a.telemetry.Telemetry, otelExporter)
	aggregateProcessorFactory := a.componentsFactory.Processors[aggregateprocessor.Type]
	aggregateProcessor := aggregateProcessorFactory.NewFunc(aggregateProcessorFactory.Config, a.telemetry.Telemetry, otelExporter)
	// 2. Kubernetes metadata processor
	k8sProcessorFactory := a.componentsFactory.Processors[k8sprocessor.K8sMetadata]
	k8sMetadataProcessor := k8sProcessorFactory.NewFunc(k8sProcessorFactory.Config, a.telemetry.Telemetry, aggregateProcessor)
	// Initialize all analyzers
	// 1. Common network request analyzer
	networkAnalyzerFactory := a.componentsFactory.Analyzers[network.Network.String()]
	// Now NetworkAnalyzer must be initialized before any other analyzers, because it will
	// use its configuration to initialize the conntracker module which is also used by others.
	networkAnalyzer := networkAnalyzerFactory.NewFunc(networkAnalyzerFactory.Config, a.telemetry.Telemetry, []consumer.Consumer{k8sMetadataProcessor})
	// 2. Layer 4 TCP events analyzer
	aggregateProcessorForTcp := aggregateProcessorFactory.NewFunc(aggregateProcessorFactory.Config, a.telemetry.Telemetry, otelExporter)
	k8sMetadataProcessor2 := k8sProcessorFactory.NewFunc(k8sProcessorFactory.Config, a.telemetry.Telemetry, aggregateProcessorForTcp)
	tcpAnalyzerFactory := a.componentsFactory.Analyzers[tcpmetricanalyzer.TcpMetric.String()]
	tcpAnalyzer := tcpAnalyzerFactory.NewFunc(tcpAnalyzerFactory.Config, a.telemetry.Telemetry, []consumer.Consumer{k8sMetadataProcessor2})
	// Initialize receiver packaged with multiple analyzers
	analyzerManager, err := analyzer.NewManager(networkAnalyzer, tcpAnalyzer)
	if err != nil {
		return fmt.Errorf("error happened while creating analyzer manager: %w", err)
	}
	a.analyzerManager = analyzerManager
	udsReceiverFactory := a.componentsFactory.Receivers[udsreceiver.Uds]
	udsReceiver := udsReceiverFactory.NewFunc(udsReceiverFactory.Config, a.telemetry.Telemetry, analyzerManager)
	a.receiver = udsReceiver
	return nil
}
