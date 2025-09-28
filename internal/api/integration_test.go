package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sabadia/svc-downloader/internal/api/deps"
	"github.com/sabadia/svc-downloader/internal/config"
	"github.com/sabadia/svc-downloader/internal/events"
	"github.com/sabadia/svc-downloader/internal/filestore"
	"github.com/sabadia/svc-downloader/internal/models"
	"github.com/sabadia/svc-downloader/internal/planner"
	"github.com/sabadia/svc-downloader/internal/ratelimit"
	"github.com/sabadia/svc-downloader/internal/repository"
	"github.com/sabadia/svc-downloader/internal/service"
	"github.com/sabadia/svc-downloader/internal/transport"
	"github.com/sabadia/svc-downloader/internal/validation"
)

func setupTestServer(t *testing.T) (*httptest.Server, *deps.Container) {
	// Create test configuration
	cfg := config.Config{
		HTTPPort:       0, // Use random port
		DataDir:        t.TempDir(),
		BadgerDir:      t.TempDir(),
		MaxBodyBytes:   25 << 20,
		DefaultQueueID: "test",
		GracefulSecs:   5 * time.Second,
	}

	// Create repository
	repo, err := repository.NewBadgerRepository(cfg.BadgerDir)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}

	// Create services
	publisher := events.NewInMemoryPublisher()
	validator := validation.NoopValidator{}
	segPlanner := planner.SimplePlanner{}
	ratelimiter := &ratelimit.SimpleRateLimiter{}
	transportClient := transport.NewHTTPClient(0)
	fileStore := filestore.NewLocalFileStore()

	// Create default queue
	ctx := context.Background()
	_ = repo.SaveQueue(ctx, &models.Queue{
		ID:          "test",
		Name:        "test",
		Concurrency: 1,
		Default:     true,
	})

	downloadSvc := service.NewDownloadService(service.DownloadDeps{
		Repo:        repo,
		Publisher:   publisher,
		Validator:   validator,
		Planner:     segPlanner,
		RateLimiter: ratelimiter,
		FileStore:   fileStore,
		Transport:   transportClient,
	})
	queueSvc := service.NewQueueService(repo)
	container := deps.New(downloadSvc, queueSvc, publisher)

	// Create server
	handler, _ := NewServer(container)
	server := httptest.NewServer(handler)

	return server, container
}

func TestAPI_Integration_Downloads(t *testing.T) {
	server, _ := setupTestServer(t)
	defer server.Close()

	client := server.Client()

	t.Run("enqueue download", func(t *testing.T) {
		reqBody := models.EnqueueDownloadRequest{
			URL: "https://httpbin.org/bytes/1024",
			File: &models.FileOptions{
				Path:     "/tmp/test",
				Filename: "test.bin",
			},
			QueueID: "test",
		}

		jsonBody, err := json.Marshal(reqBody)
		if err != nil {
			t.Fatalf("Failed to marshal request: %v", err)
		}

		resp, err := client.Post(server.URL+"/downloads", "application/json", bytes.NewReader(jsonBody))
		if err != nil {
			t.Fatalf("POST /downloads error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("POST /downloads status = %d, want %d", resp.StatusCode, http.StatusOK)
		}

		var result models.EnqueueDownloadResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if result.ID == "" {
			t.Error("Download ID should not be empty")
		}
		if result.QueueID != "test" {
			t.Errorf("QueueID = %s, want test", result.QueueID)
		}
		if result.Status != models.StatusQueued {
			t.Errorf("Status = %s, want %s", result.Status, models.StatusQueued)
		}
	})

	t.Run("list downloads", func(t *testing.T) {
		resp, err := client.Get(server.URL + "/downloads")
		if err != nil {
			t.Fatalf("GET /downloads error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET /downloads status = %d, want %d", resp.StatusCode, http.StatusOK)
		}

		var result []models.Download
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if len(result) == 0 {
			t.Error("Expected at least one download")
		}
	})

	t.Run("get download by ID", func(t *testing.T) {
		// First create a download
		reqBody := models.EnqueueDownloadRequest{
			URL: "https://httpbin.org/bytes/512",
			File: &models.FileOptions{
				Path:     "/tmp/test",
				Filename: "test2.bin",
			},
			QueueID: "test",
		}

		jsonBody, err := json.Marshal(reqBody)
		if err != nil {
			t.Fatalf("Failed to marshal request: %v", err)
		}

		createResp, err := client.Post(server.URL+"/downloads", "application/json", bytes.NewReader(jsonBody))
		if err != nil {
			t.Fatalf("POST /downloads error: %v", err)
		}
		defer createResp.Body.Close()

		var createResult models.EnqueueDownloadResponse
		if err := json.NewDecoder(createResp.Body).Decode(&createResult); err != nil {
			t.Fatalf("Failed to decode create response: %v", err)
		}

		// Now get the download by ID
		resp, err := client.Get(server.URL + "/downloads/" + createResult.ID)
		if err != nil {
			t.Fatalf("GET /downloads/{id} error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET /downloads/{id} status = %d, want %d", resp.StatusCode, http.StatusOK)
		}

		var result models.Download
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if result.ID != createResult.ID {
			t.Errorf("Download ID = %s, want %s", result.ID, createResult.ID)
		}
	})

	t.Run("count downloads", func(t *testing.T) {
		resp, err := client.Get(server.URL + "/downloads/count")
		if err != nil {
			t.Fatalf("GET /downloads/count error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET /downloads/count status = %d, want %d", resp.StatusCode, http.StatusOK)
		}

		var result int
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if result < 0 {
			t.Errorf("Count = %d, want >= 0", result)
		}
	})
}

func TestAPI_Integration_Queues(t *testing.T) {
	server, _ := setupTestServer(t)
	defer server.Close()

	client := server.Client()

	t.Run("create queue", func(t *testing.T) {
		reqBody := models.Queue{
			ID:          "test-queue",
			Name:        "Test Queue",
			Concurrency: 5,
			RateLimit:   1000000, // 1MB/s
			Default:     false,
		}

		jsonBody, err := json.Marshal(reqBody)
		if err != nil {
			t.Fatalf("Failed to marshal request: %v", err)
		}

		resp, err := client.Post(server.URL+"/queues", "application/json", bytes.NewReader(jsonBody))
		if err != nil {
			t.Fatalf("POST /queues error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("POST /queues status = %d, want %d", resp.StatusCode, http.StatusNoContent)
		}
	})

	t.Run("list queues", func(t *testing.T) {
		resp, err := client.Get(server.URL + "/queues")
		if err != nil {
			t.Fatalf("GET /queues error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET /queues status = %d, want %d", resp.StatusCode, http.StatusOK)
		}

		var result []models.Queue
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if len(result) == 0 {
			t.Error("Expected at least one queue")
		}
	})

	t.Run("get queue by ID", func(t *testing.T) {
		resp, err := client.Get(server.URL + "/queues/test")
		if err != nil {
			t.Fatalf("GET /queues/{id} error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET /queues/{id} status = %d, want %d", resp.StatusCode, http.StatusOK)
		}

		var result models.Queue
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if result.ID != "test" {
			t.Errorf("Queue ID = %s, want test", result.ID)
		}
	})
}

func TestAPI_Integration_ErrorHandling(t *testing.T) {
	server, _ := setupTestServer(t)
	defer server.Close()

	client := server.Client()

	t.Run("invalid limit parameter", func(t *testing.T) {
		resp, err := client.Get(server.URL + "/downloads?limit=invalid")
		if err != nil {
			t.Fatalf("GET /downloads?limit=invalid error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET /downloads?limit=invalid status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
	})

	t.Run("invalid offset parameter", func(t *testing.T) {
		resp, err := client.Get(server.URL + "/downloads?offset=invalid")
		if err != nil {
			t.Fatalf("GET /downloads?offset=invalid error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET /downloads?offset=invalid status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
	})

	t.Run("non-existent download", func(t *testing.T) {
		resp, err := client.Get(server.URL + "/downloads/non-existent-id")
		if err != nil {
			t.Fatalf("GET /downloads/non-existent-id error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET /downloads/non-existent-id status = %d, want %d", resp.StatusCode, http.StatusNotFound)
		}
	})
}

func TestAPI_Integration_OpenAPI(t *testing.T) {
	server, _ := setupTestServer(t)
	defer server.Close()

	client := server.Client()

	t.Run("get OpenAPI spec", func(t *testing.T) {
		resp, err := client.Get(server.URL + "/openapi.yaml")
		if err != nil {
			t.Fatalf("GET /openapi.yaml error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET /openapi.yaml status = %d, want %d", resp.StatusCode, http.StatusOK)
		}

		contentType := resp.Header.Get("Content-Type")
		if contentType != "application/x-yaml" {
			t.Errorf("Content-Type = %s, want application/x-yaml", contentType)
		}

		// Read a bit of the response to ensure it's valid YAML
		buf := make([]byte, 200)
		n, err := resp.Body.Read(buf)
		if err != nil && err.Error() != "EOF" {
			t.Fatalf("Failed to read OpenAPI response: %v", err)
		}

		content := string(buf[:n])
		if !contains(content, "openapi:") && !contains(content, "info:") && !contains(content, "components:") {
			t.Errorf("OpenAPI response doesn't look like valid YAML: %s", content)
		}
	})
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[:len(substr)] == substr
}
