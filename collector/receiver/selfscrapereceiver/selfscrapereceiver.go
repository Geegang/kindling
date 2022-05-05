package selfscrapereceiver

import (
	analyzerpackage "github.com/Kindling-project/kindling/collector/analyzer"
	"github.com/Kindling-project/kindling/collector/component"
	"github.com/Kindling-project/kindling/collector/receiver"
	"time"
)

const (
	Type string = "selfscrapereceiver"
)

type SelfScrapeReceiver struct {
	analyzerManager analyzerpackage.Manager
	telemetry       *component.TelemetryTools
	close           chan bool
}

type Config struct {
}

func New(config interface{}, telemetry *component.TelemetryTools, analyzerManager analyzerpackage.Manager) receiver.Receiver {
	return &SelfScrapeReceiver{
		analyzerManager: analyzerManager,
		telemetry:       telemetry,
	}
}

// Start initializes the receiver and start to receive events.
func (c *SelfScrapeReceiver) Start() error {
	ticker := time.NewTicker(5 * time.Second)
	go func() {
		for {
			select {
			case <-c.close:
				return
			case <-ticker.C:
				// do something
			}
		}
	}()
	return nil
}

// Shutdown closes the receiver and stops receiving events.
// Note receiver should not shutdown other components though it holds a reference
func (c *SelfScrapeReceiver) Shutdown() error {
	c.close <- true
	return nil
}
