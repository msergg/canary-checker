package checks

import (
	"encoding/json"
	"strconv"

	"github.com/flanksource/canary-checker/api/context"
	v1 "github.com/flanksource/canary-checker/api/v1"
	"github.com/flanksource/canary-checker/pkg"
	"github.com/flanksource/commons/logger"
	"github.com/prometheus/client_golang/prometheus"
)

var collectorMap = make(map[string]prometheus.Collector)

func addPrometheusMetric(name, metricType string, labelNames []string) prometheus.Collector {
	var collector prometheus.Collector
	switch metricType {
	case "histogram":
		collector = prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: name},
			labelNames,
		)
	case "counter":
		collector = prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: name},
			labelNames,
		)
	case "gauge":
		collector = prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: name},
			labelNames,
		)
	default:
		return nil
	}

	collectorMap[name] = collector
	prometheus.MustRegister(collector)
	return collector
}

func exportCheckMetrics(ctx *context.Context, results pkg.Results) {
	if len(results) == 0 {
		return
	}

	for _, r := range results {
		for _, spec := range r.Check.GetMetricsSpec() {
			if spec.Name == "" || spec.Value == "" {
				continue
			}

			var collector prometheus.Collector
			var exists bool
			if collector, exists = collectorMap[spec.Name]; !exists {
				collector = addPrometheusMetric(spec.Name, spec.Type, spec.Labels.Names())
				if collector == nil {
					logger.Errorf("Invalid type for check.metrics %s for check[%s]", spec.Type, r.Check.GetName())
					continue
				}
			}

			// Convert result Data into JSON for templating
			var rData map[string]any
			resultBytes, err := json.Marshal(r.Data)
			if err != nil {
				logger.Errorf("Error converting check result data into json: %v", err)
				continue
			}
			if err := json.Unmarshal(resultBytes, &rData); err != nil {
				logger.Errorf("Error converting check result data into json: %v", err)
				continue
			}

			tplValue := v1.Template{Expression: spec.Value}
			templateInput := map[string]any{
				"result": rData,
				"check": map[string]any{
					"name":        r.Check.GetName(),
					"description": r.Check.GetDescription(),
					"labels":      r.Check.GetLabels(),
					"endpoint":    r.Check.GetEndpoint(),
					"duration":    r.GetDuration(),
				},
			}

			valRaw, err := template(ctx.New(templateInput), tplValue)
			if err != nil {
				logger.Errorf("Error templating value for check.metrics template %s for check[%s]: %v", spec.Value, r.Check.GetName(), err)
				continue
			}
			val, err := strconv.ParseFloat(valRaw, 64)
			if err != nil {
				logger.Errorf("Error converting value %s to float for check.metrics template %s for check[%s]: %v", valRaw, spec.Value, r.Check.GetName(), err)
				continue
			}

			var orderedLabelVals []string
			for _, label := range spec.Labels {
				val := label.Value
				if label.ValueExpr != "" {
					var err error
					val, err = template(ctx.New(templateInput), v1.Template{Expression: label.ValueExpr})
					if err != nil {
						logger.Errorf("Error templating label %s:%s for check.metrics for check[%s]: %v", label.Name, label.ValueExpr, r.Check.GetName(), err)
					}
				}
				orderedLabelVals = append(orderedLabelVals, val)
			}

			switch collector := collector.(type) {
			case *prometheus.HistogramVec:
				collector.WithLabelValues(orderedLabelVals...).Observe(val)
			case *prometheus.GaugeVec:
				collector.WithLabelValues(orderedLabelVals...).Set(val)
			case *prometheus.CounterVec:
				if val <= 0 {
					continue
				}
				collector.WithLabelValues(orderedLabelVals...).Add(val)
			default:
				logger.Errorf("Got unknown type for check.metrics %T", collector)
			}
		}
	}
}
