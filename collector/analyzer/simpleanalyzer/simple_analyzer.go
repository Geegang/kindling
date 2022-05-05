package simpleanalyzer

import (
	"fmt"
	"github.com/Kindling-project/kindling/collector/analyzer"
	"github.com/Kindling-project/kindling/collector/component"
	"github.com/Kindling-project/kindling/collector/consumer"
	"github.com/Kindling-project/kindling/collector/model"
	"github.com/hashicorp/go-multierror"
)

const (
	Type analyzer.Type = "simpleanalyzer"
)

type SimpleAnalyzer struct {
	consumers []consumer.Consumer

	telemetry *component.TelemetryTools
}

type Config struct {
}

func New(cfg interface{}, telemetry *component.TelemetryTools, nextConsumers []consumer.Consumer) analyzer.Analyzer {
	return &SimpleAnalyzer{
		consumers: nextConsumers,
		telemetry: telemetry,
	}
}

// Start initializes the analyzer
func (a *SimpleAnalyzer) Start() error {
	return nil
}

// ConsumeEvent gets the event from the previous component
func (a *SimpleAnalyzer) ConsumeEvent(event *model.KindlingEvent) error {
	gaugeGroup, err := getSimpleGaugeGroup(event)
	if err != nil {
		return err
	}
	var retError error
	for _, nextConsumer := range a.consumers {
		err := nextConsumer.Consume(gaugeGroup)
		if err != nil {
			retError = multierror.Append(retError, err)
		}
	}
	return retError
}

// getSimpleGaugeGroup generates model.GaugeGroup by
func getSimpleGaugeGroup(event *model.KindlingEvent) (*model.GaugeGroup, error) {
	if event == nil {
		return nil, fmt.Errorf("event cannot be nil")
	}

	labels := model.NewAttributeMap()
	var value *model.Gauge
	for _, keyValue := range event.UserAttributes {
		switch keyValue.Key {
		case "metric_name":
			value = &model.Gauge{
				Name:  keyValue.Key,
				Value: keyValue.GetIntValue(),
			}
		default:
			labels.AddStringValue(keyValue.Key, string(keyValue.GetValue()))
		}
	}
	return model.NewGaugeGroup("simple_gauge_group", labels, event.Timestamp, value), nil
}

// Shutdown cleans all the resources used by the analyzer
func (a *SimpleAnalyzer) Shutdown() error {
	return nil
}

// Type returns the type of the analyzer
func (a *SimpleAnalyzer) Type() analyzer.Type {
	return Type
}
