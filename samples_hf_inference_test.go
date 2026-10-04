package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Coverage for the shipped Hugging Face Inference Endpoints sample (samples/hf-inference/hf-inference.xml).

const hfInferenceSampleConfig = "samples/hf-inference/hf-inference.xml"

// hfFakeToken is not a credential. The stub only checks that it arrives.
const hfFakeToken = "hf_stub_token_for_tests"

const (
	hfRunningEndpointJSON = `{
		"name":"llama-chat","type":"authenticated",
		"provider":{"vendor":"aws","region":"us-east-1"},
		"compute":{"accelerator":"gpu","instanceType":"nvidia-l4","instanceSize":"x1",
			"scaling":{"minReplica":0,"maxReplica":1,"scaleToZeroTimeout":15}},
		"model":{"repository":"meta-llama/Llama-3.1-8B-Instruct","framework":"pytorch","task":"text-generation","image":{"huggingface":{}}},
		"status":{"state":"running","message":"Endpoint is ready","url":"https://abc123.us-east-1.aws.endpoints.huggingface.cloud",
			"readyReplica":1,"targetReplica":1,"createdAt":"2026-10-01T12:00:00Z","updatedAt":"2026-10-02T12:00:00Z",
			"createdBy":{"id":"1","name":"acme"},"updatedBy":{"id":"1","name":"acme"}},
		"tags":["chat"],"healthRoute":"/health","experimentalFeatures":{},"notifications":{}}`

	hfPausedEndpointJSON = `{
		"name":"bge-embed","type":"private",
		"provider":{"vendor":"aws","region":"eu-west-1"},
		"compute":{"accelerator":"cpu","instanceType":"intel-spr","instanceSize":"x2",
			"scaling":{"minReplica":1,"maxReplica":2,"scaleToZeroTimeout":null}},
		"model":{"repository":"BAAI/bge-small-en-v1.5","framework":"pytorch","task":"sentence-embeddings","image":{"huggingface":{}}},
		"status":{"state":"paused","message":"Endpoint paused","url":"",
			"readyReplica":0,"targetReplica":0,"createdAt":"2026-09-01T12:00:00Z","updatedAt":"2026-10-03T08:00:00Z",
			"createdBy":{"id":"1","name":"acme"},"updatedBy":{"id":"1","name":"acme"}},
		"tags":[],"healthRoute":"/health","experimentalFeatures":{},"notifications":{}}`

	hfListJSON = `{"items":[` + hfRunningEndpointJSON + `,` + hfPausedEndpointJSON + `],"nextCursor":"c2","prevCursor":null,"totalItems":3}`
)

// hfRequest is what the stub saw of one call.
type hfRequest struct {
	method, path, query, auth, contentType, body string
}

// hfStub records every request it answers.
type hfStub struct {
	mu   sync.Mutex
	seen []hfRequest
}

func (s *hfStub) requests() []hfRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]hfRequest(nil), s.seen...)
}

func (s *hfStub) only(t *testing.T) hfRequest {
	t.Helper()
	got := s.requests()
	require.Len(t, got, 1, "exactly one request reaches the API")
	return got[0]
}

// hfInferenceSampleWithServer loads the real sample with its base URL pointed
// at a stub of the v2 API.
func hfInferenceSampleWithServer(t *testing.T) (*Config, *hfStub) {
	t.Helper()
	stub := &hfStub{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		stub.mu.Lock()
		stub.seen = append(stub.seen, hfRequest{
			method:      r.Method,
			path:        r.URL.Path,
			query:       r.URL.RawQuery,
			auth:        r.Header.Get("Authorization"),
			contentType: r.Header.Get("Content-Type"),
			body:        string(body),
		})
		stub.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /v2/endpoint/acme":
			_, _ = io.WriteString(w, hfListJSON)
		case "POST /v2/endpoint/acme/bge-embed/pause":
			_, _ = io.WriteString(w, hfPausedEndpointJSON)
		case "POST /v2/endpoint/acme", "PUT /v2/endpoint/acme/llama-chat":
			_, _ = io.WriteString(w, hfRunningEndpointJSON)
		default:
			http.Error(w, `{"error":"no stub for this route"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	t.Setenv("HF_ENDPOINTS_API_URL", srv.URL+"/v2")
	t.Setenv("HF_TOKEN", hfFakeToken)
	t.Setenv("HUGGING_FACE_HUB_TOKEN", "")
	swapHTTPClient(t, srv)

	cfg, err := Load(hfInferenceSampleConfig)
	require.NoError(t, err, "the shipped Hugging Face sample must load and validate")
	return cfg, stub
}

// hfLeaf resolves a leaf by its MCP tool name, with the same inheritance
// buildMCPServer applies, preconditions included.
func hfLeaf(t *testing.T, cfg *Config, name string) *mcpLeaf {
	t.Helper()
	leaves := collectMCPLeaves(cfg.Commands, mcpInherit{
		vars:    cfg.Vars,
		pre:     cfg.Preconditions,
		cmd:     cfg.Command,
		request: cfg.Request,
		cwd:     cfg.Cwd,
		stdin:   cfg.Stdin,
		formats: cfg.Formats,
	})
	for i := range leaves {
		if leaves[i].name == name {
			return &leaves[i]
		}
	}
	t.Fatalf("leaf %q not found in %s", name, hfInferenceSampleConfig)
	return nil
}

func TestHFInferenceSampleLoads(t *testing.T) {
	cfg, err := Load(hfInferenceSampleConfig)
	require.NoError(t, err)
	assert.Equal(t, "hf-inference", cfg.Name)
}

func TestHFInferenceSample_List_Table(t *testing.T) {
	cfg, stub := hfInferenceSampleWithServer(t)

	code, out := execCmd(t, cfg, "list", "acme", "--state", "running,paused", "--as", "table")
	require.Equalf(t, 0, code, "list should succeed; got: %s", out)

	req := stub.only(t)
	assert.Equal(t, "GET", req.method)
	assert.Equal(t, "/v2/endpoint/acme", req.path)
	assert.Equal(t, "limit=20&state=running%2Cpaused", req.query)
	assert.Equal(t, "Bearer "+hfFakeToken, req.auth)

	assert.Contains(t, out, "llama-chat")
	assert.Contains(t, out, "running")
	assert.Contains(t, out, "bge-embed")
	assert.Contains(t, out, "paused")
	assert.Contains(t, out, "3 total, next page: --cursor c2")
	assert.NotContains(t, out, "<no value>")
}

func TestHFInferenceSample_List_MCP(t *testing.T) {
	cfg, stub := hfInferenceSampleWithServer(t)

	out, isErr := mcpExecLeaf(hfLeaf(t, cfg, "list"), map[string]any{"namespace": "acme"})
	require.Falsef(t, isErr, "list should succeed; got: %s", out)

	assert.Equal(t, "/v2/endpoint/acme", stub.only(t).path)
	assert.Contains(t, out, "llama-chat")
	assert.Contains(t, out, "bge-embed")
	assert.NotContains(t, out, "<no value>")
}

func TestHFInferenceSample_Pause(t *testing.T) {
	cfg, stub := hfInferenceSampleWithServer(t)

	code, out := execCmd(t, cfg, "pause", "acme", "bge-embed", "--as", "json")
	require.Equalf(t, 0, code, "pause should succeed; got: %s", out)

	req := stub.only(t)
	assert.Equal(t, "POST", req.method)
	assert.Equal(t, "/v2/endpoint/acme/bge-embed/pause", req.path)
	assert.Empty(t, req.body)
	assert.Equal(t, "Bearer "+hfFakeToken, req.auth)

	var got map[string]any
	require.NoErrorf(t, json.Unmarshal([]byte(out), &got), "pause --as json prints one object; got: %s", out)
	assert.Equal(t, map[string]any{
		"name":     "bge-embed",
		"state":    "paused",
		"message":  "Endpoint paused",
		"replicas": "0/0",
	}, got)
}

func TestHFInferenceSample_Create_ScalesToZeroByDefault(t *testing.T) {
	cfg, stub := hfInferenceSampleWithServer(t)

	code, out := execCmd(t, cfg, "create", "acme", "llama-chat",
		"--repository", "meta-llama/Llama-3.1-8B-Instruct", "--framework", "pytorch",
		"--accelerator", "gpu", "--instance-type", "nvidia-l4", "--instance-size", "x1",
		"--vendor", "aws", "--region", "us-east-1", "--type", "authenticated",
		"--task", "text-generation", "--tag", "chat")
	require.Equalf(t, 0, code, "create should succeed; got: %s", out)

	req := stub.only(t)
	assert.Equal(t, "POST", req.method)
	assert.Equal(t, "/v2/endpoint/acme", req.path)
	assert.Equal(t, "application/json", req.contentType)
	assert.JSONEq(t, `{
		"name":"llama-chat","type":"authenticated",
		"provider":{"vendor":"aws","region":"us-east-1"},
		"compute":{"accelerator":"gpu","instanceType":"nvidia-l4","instanceSize":"x1",
			"scaling":{"minReplica":0,"maxReplica":1,"scaleToZeroTimeout":15}},
		"model":{"repository":"meta-llama/Llama-3.1-8B-Instruct","framework":"pytorch",
			"task":"text-generation","image":{"huggingface":{}}},
		"tags":["chat"]}`, req.body)
}

func TestHFInferenceSample_Create_NoTimeoutAboveZeroReplicas(t *testing.T) {
	cfg, stub := hfInferenceSampleWithServer(t)

	code, out := execCmd(t, cfg, "create", "acme", "llama-chat",
		"--repository", "meta-llama/Llama-3.1-8B-Instruct", "--framework", "custom",
		"--accelerator", "gpu", "--instance-type", "nvidia-l4", "--instance-size", "x1",
		"--vendor", "aws", "--region", "us-east-1", "--type", "private",
		"--min-replica", "1", "--image", `{"custom":{"url":"ghcr.io/acme/server:1","port":8080}}`)
	require.Equalf(t, 0, code, "create should succeed; got: %s", out)

	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(stub.only(t).body), &body))
	compute := body["compute"].(map[string]any)
	assert.Equal(t, map[string]any{"minReplica": float64(1), "maxReplica": float64(1)}, compute["scaling"])
	model := body["model"].(map[string]any)
	assert.Equal(t, map[string]any{"custom": map[string]any{"url": "ghcr.io/acme/server:1", "port": float64(8080)}}, model["image"])
	assert.NotContains(t, body, "tags")
	assert.NotContains(t, model, "task")
}

func TestHFInferenceSample_Update_SendsOnlyTheFlagsGiven(t *testing.T) {
	cfg, stub := hfInferenceSampleWithServer(t)

	code, out := execCmd(t, cfg, "update", "acme", "llama-chat", "--min-replica", "0", "--scale-to-zero-timeout", "30", "--revision", "abc123")
	require.Equalf(t, 0, code, "update should succeed; got: %s", out)

	req := stub.only(t)
	assert.Equal(t, "PUT", req.method)
	assert.Equal(t, "/v2/endpoint/acme/llama-chat", req.path)
	assert.JSONEq(t, `{"model":{"revision":"abc123"},"compute":{"scaling":{"minReplica":0,"scaleToZeroTimeout":30}}}`, req.body)
}

func TestHFInferenceSample_NoTokenSendsNothing(t *testing.T) {
	cfg, stub := hfInferenceSampleWithServer(t)
	t.Setenv("HF_TOKEN", "")

	code, _, stderr := execCmdFull(t, cfg, "pause", "acme", "bge-embed")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "set HF_TOKEN")
	assert.Empty(t, stub.requests())
}
