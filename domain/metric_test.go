package domain

import "testing"
import "net/http"

func TestNewBuilder(t *testing.T) {
	build, err := NewMetricConfigBuilder("http://localhost", "POST")
	if err != nil || build == nil {
		t.Fatalf("Failed Creating metric builder: build=%+v, err=%+v", build, err)
	}

	build, err = NewMetricConfigBuilder(":localhost", "POST")
	if err == nil || build != nil {
		t.Fatalf("Expected Error Creating metric builder: build=%+v, err=%+v", build, err)
	}

	build, err = NewMetricConfigBuilder("http://localhost", "?")
	if err == nil || build != nil {
		t.Fatalf("Expected Error Creating metric builder: build=%+v, err=%+v", build, err)
	}
}

func TestBuilder(t *testing.T) {
	build, _ := NewMetricConfigBuilder("http://localhost", "POST")
	build.Metric("metric_0", "metric_name_0").SetTag("tag", "value-0")
	config, err := build.Config("metric_group", "metric_group_name", "metric_group_description", "1h-ago")
	if err != nil {
		t.Fatalf("Error building config=%+v, err=%+v", config, err)
	}

	headers := make(http.Header)
	headers["Content-Type"] = []string{"application/json"}
	expected := MetricConfig{
		ID:          "metric_group",
		Name:        "metric_group_name",
		Description: "metric_group_description",
		Query: QueryConfig{
			URL:     "http://localhost",
			Method:  "POST",
			Headers: headers,
			Data:    "{\"metrics\":[{\"metric\":\"metric_0\",\"tags\":{\"tag\":[\"value-0\"]}}],\"start\":\"1h-ago\"}",
		},
		Metrics: []Metric{Metric{
			ID:   "metric_0",
			Name: "metric_name_0",
		}}}

	if !expected.Equals(config) {
		t.Logf("Config does not match expected")
		t.Logf("expected=%+v", expected)
		t.Fatalf("config=%+v", config)
	}
}
