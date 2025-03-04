package llmobs

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// Enable initializes and starts the LLM Observability SDK.
// It configures the SDK with the provided options and begins collecting
// LLM-related spans and metrics.
func Enable(opts Options) error {
	llmObs := GetLLMObs()
	llmObs.Lock()
	defer llmObs.Unlock()

	if llmObs.enabled {
		log.Debug("LLMObs already enabled")
		return nil
	}

	// Check if disabled via environment variable
	if envVal, exists := os.LookupEnv("DD_LLMOBS_ENABLED"); exists {
		if envVal == "0" || envVal == "false" {
			log.Debug("LLMObs disabled by environment variable DD_LLMOBS_ENABLED")
			return nil
		}
	}

	// Set configuration from options and environment variables
	if opts.MLApp != "" {
		llmObs.mlApp = opts.MLApp
	}
	if opts.IntegrationsEnabled {
		llmObs.integrationsEnabled = opts.IntegrationsEnabled
	}
	if opts.AgentlessEnabled {
		llmObs.agentlessEnabled = opts.AgentlessEnabled
	}
	if opts.Site != "" {
		llmObs.site = opts.Site
	}
	if opts.APIKey != "" {
		llmObs.apiKey = opts.APIKey
	}
	if opts.Env != "" {
		llmObs.env = opts.Env
	}
	if opts.Service != "" {
		llmObs.service = opts.Service
	}

	// Validate required configuration
	if llmObs.mlApp == "" {
		return fmt.Errorf("DD_LLMOBS_ML_APP is required for sending LLMObs data")
	}

	if llmObs.agentlessEnabled {
		// Validate agentless configuration
		if llmObs.apiKey == "" {
			return fmt.Errorf("DD_API_KEY is required when agentless mode is enabled")
		}
		if llmObs.site == "" {
			return fmt.Errorf("DD_SITE is required when agentless mode is enabled")
		}
	}

	// Initialize the span and metrics writers
	writerInterval := getEnvFloat("_DD_LLMOBS_WRITER_INTERVAL", 1.0)
	writerTimeout := getEnvFloat("_DD_LLMOBS_WRITER_TIMEOUT", 5.0)

	// Initialize writers
	llmObs.spanWriter = newLLMObsSpanWriter(
		llmObs.agentlessEnabled,
		fmt.Sprintf("https://llmobs-intake.%s", llmObs.site),
		writerInterval,
		writerTimeout,
	)

	llmObs.evalMetricWriter = newLLMObsEvalMetricWriter(
		llmObs.site,
		llmObs.apiKey,
		writerInterval,
		writerTimeout,
	)

	// Initialize evaluator runner
	evaluatorInterval := getEnvFloat("_DD_LLMOBS_EVALUATOR_INTERVAL", 1.0)
	llmObs.evaluatorRunner = newEvaluatorRunner(evaluatorInterval, llmObs)

	// Start the services
	err := llmObs.startServices()
	if err != nil {
		return fmt.Errorf("failed to start LLMObs services: %w", err)
	}

	// Enable integrations if configured
	if llmObs.integrationsEnabled {
		patchIntegrations()
	}

	llmObs.enabled = true
	log.Debug("LLMObs enabled")
	return nil
}

// Disable stops the LLM Observability SDK and flushes any remaining data.
func Disable() {
	llmObs := GetLLMObs()
	llmObs.Lock()
	defer llmObs.Unlock()

	if !llmObs.enabled {
		log.Debug("LLMObs already disabled")
		return
	}

	// Flush remaining data
	Flush()

	// Stop services
	llmObs.stopServices()

	llmObs.enabled = false
	log.Debug("LLMObs disabled")
}

// Flush flushes any remaining spans and evaluation metrics.
func Flush() {
	llmObs := GetLLMObs()
	llmObs.RLock()
	defer llmObs.RUnlock()

	if !llmObs.enabled {
		log.Debug("Flush called when LLMObs is disabled")
		return
	}

	// Run evaluator
	if llmObs.evaluatorRunner != nil {
		llmObs.evaluatorRunner.periodic()
	}

	// Flush spans and metrics
	if llmObs.spanWriter != nil {
		llmObs.spanWriter.periodic()
	}
	if llmObs.evalMetricWriter != nil {
		llmObs.evalMetricWriter.periodic()
	}
}

// IsEnabled returns whether LLMObs is currently enabled.
func IsEnabled() bool {
	llmObs := GetLLMObs()
	llmObs.RLock()
	defer llmObs.RUnlock()
	return llmObs.enabled
}

// startServices starts the LLMObs services.
func (l *LLMObs) startServices() error {
	var errors []error

	// Start span writer
	if l.spanWriter != nil {
		if err := l.spanWriter.start(); err != nil {
			errors = append(errors, fmt.Errorf("failed to start span writer: %w", err))
		}
	}

	// Start eval metric writer
	if l.evalMetricWriter != nil {
		if err := l.evalMetricWriter.start(); err != nil {
			errors = append(errors, fmt.Errorf("failed to start eval metric writer: %w", err))
		}
	}

	// Start evaluator runner
	if l.evaluatorRunner != nil {
		if err := l.evaluatorRunner.start(); err != nil {
			errors = append(errors, fmt.Errorf("failed to start evaluator runner: %w", err))
		}
	}

	if len(errors) > 0 {
		// Return the first error but log all errors
		for _, err := range errors {
			log.Error("Error starting LLMObs service: %v", err)
		}
		return errors[0]
	}

	return nil
}

// stopServices stops the LLMObs services.
func (l *LLMObs) stopServices() {
	// Stop evaluator runner
	if l.evaluatorRunner != nil {
		if err := l.evaluatorRunner.stop(); err != nil {
			log.Debug("Error stopping evaluator runner: %v", err)
		}
	}

	// Stop span writer
	if l.spanWriter != nil {
		if err := l.spanWriter.stop(); err != nil {
			log.Debug("Error stopping span writer: %v", err)
		}
	}

	// Stop eval metric writer
	if l.evalMetricWriter != nil {
		if err := l.evalMetricWriter.stop(); err != nil {
			log.Debug("Error stopping eval metric writer: %v", err)
		}
	}
}

// Helper function to parse float environment variables with a default value.
func getEnvFloat(key string, defaultValue float64) float64 {
	if val, exists := os.LookupEnv(key); exists {
		if parsedVal, err := strconv.ParseFloat(val, 64); err == nil {
			return parsedVal
		}
	}
	return defaultValue
}

// patchIntegrations enables the supported LLM integrations.
func patchIntegrations() {
	// This would integrate with Datadog's patching mechanism
	// for the supported integrations like OpenAI, Anthropic, etc.
	log.Debug("Patching LLM integrations")
	// Implementation would depend on Datadog's patching mechanism
}

// Helper function to generate a random 64-bit identifier
func rand64bits() uint64 {
	// Simple implementation using time and random number
	// In a real implementation, use a more robust random number generator
	return uint64(time.Now().UnixNano())
}
