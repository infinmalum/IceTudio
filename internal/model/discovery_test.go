package model

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"icetudio/internal/config"
)

type transport func(*http.Request) (*http.Response, error)

func (t transport) RoundTrip(req *http.Request) (*http.Response, error) { return t(req) }

func TestListModels(t *testing.T) {
	client := &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.String() != "http://127.0.0.1:1234/v1/models" {
			t.Fatalf("wrong request: %s %s", req.Method, req.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"zeta"},{"id":"alpha"}]}`)), Header: http.Header{}}, nil
	})}
	got, err := (Discovery{Client: client}).List(context.Background(), config.DefaultModel("lmstudio"))
	if err != nil || len(got) != 2 || got[0] != "alpha" || got[1] != "zeta" {
		t.Fatalf("models: %+v, %v", got, err)
	}
}

func TestListModelsError(t *testing.T) {
	client := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("unavailable")), Header: http.Header{}}, nil
	})}
	if _, err := (Discovery{Client: client}).List(context.Background(), config.DefaultModel("lmstudio")); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("missing status error: %v", err)
	}
}
