package directclient

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ensingerphilipp/premiumizearr-nova/internal/config"
	"github.com/ensingerphilipp/premiumizearr-nova/pkg/premiumizeme"
)

// This exercises the real manager and Premiumize HTTP client as one flow.
// The fake server owns all Premiumize endpoints and the generated file URL;
// no account, sleep-based scheduler, or live Premiumize service is involved.
func TestManagerSubmissionRestartProgressAndCompletedDownload(t *testing.T) {
	const (
		magnet   = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Example.Release"
		transfer = "transfer-1"
		folder   = "job-folder-1"
		payload  = "finished media payload"
	)
	var transferStatus atomic.Value
	transferStatus.Store("downloading")
	var createCount atomic.Int32
	var folderCount atomic.Int32
	var api *httptest.Server
	api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/media" && r.URL.Query().Get("apikey") != "test-key" {
			t.Errorf("%s %s missing API key", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/account/info":
			fmt.Fprint(w, `{"status":"success","limit_used":0,"booster_points":0}`)
		case "/api/folder/list":
			if id := r.URL.Query().Get("id"); id == folder {
				fmt.Fprint(w, `{"status":"success","content":[{"id":"file-1","name":"episode.mkv","type":"file"}]}`)
			} else {
				fmt.Fprint(w, `{"status":"success","content":[]}`)
			}
		case "/api/folder/create":
			if folderCount.Add(1) == 1 {
				if got := r.URL.Query().Get("name"); got != "arrDownloads-direct" {
					t.Errorf("root folder name = %q, want arrDownloads-direct", got)
				}
				fmt.Fprint(w, `{"status":"success","id":"direct-root"}`)
			} else {
				if got := r.URL.Query().Get("name"); got != "0123456789abcdef0123456789abcdef01234567" {
					t.Errorf("job folder name = %q, want torrent hash", got)
				}
				if got := r.URL.Query().Get("parent_id"); got != "direct-root" {
					t.Errorf("job folder parent = %q, want direct-root", got)
				}
				fmt.Fprint(w, `{"status":"success","id":"job-folder-1"}`)
			}
		case "/api/transfer/create":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse transfer submission: %v", err)
			}
			if got := r.FormValue("src"); got != magnet {
				t.Errorf("submitted source = %q, want magnet payload", got)
			}
			if got := r.FormValue("folder_id"); got != folder {
				t.Errorf("submission folder_id = %q, want %q", got, folder)
			}
			createCount.Add(1)
			fmt.Fprintf(w, `{"status":"success","id":%q,"name":"Example.Release","type":"torrent"}`, transfer)
		case "/api/transfer/list":
			fmt.Fprintf(w, `{"status":"success","transfers":[{"id":%q,"name":"Example.Release","status":%q,"progress":0.5}]}`, transfer, transferStatus.Load().(string))
		case "/api/item/details":
			if r.URL.Query().Get("id") != "file-1" {
				t.Errorf("details id = %q, want file-1", r.URL.Query().Get("id"))
			}
			fmt.Fprintf(w, `{"status":"success","id":"file-1","name":"episode.mkv","type":"file","link":%q}`, api.URL+"/media")
		case "/media":
			w.Header().Set("Content-Type", "application/octet-stream")
			fmt.Fprint(w, payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	// Several legacy Premiumize methods create their own http.Client and one
	// uses http.DefaultClient. Those clients all use DefaultTransport when no
	// explicit transport is set, so route every request to this httptest server.
	oldTransport := http.DefaultTransport
	http.DefaultTransport = api.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = oldTransport })

	configDir := t.TempDir()
	downloadsDir := filepath.Join(t.TempDir(), "downloads")
	if err := os.MkdirAll(downloadsDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		TransferDirectory:     "arrDownloads",
		DownloadsDirectory:    downloadsDir,
		SimultaneousDownloads: 1,
		EnableTlsCheck:        true,
	}
	pm := premiumizeme.NewPremiumizemeClient("test-key")
	pm.APIBaseURL = api.URL + "/api/"
	pm.HTTPClient = api.Client()

	manager, err := NewManager(&pm, cfg, configDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AddMagnet(context.Background(), magnet, "tv"); err != nil {
		t.Fatalf("queue magnet: %v", err)
	}
	wantID := "0123456789abcdef0123456789abcdef01234567"
	queued := manager.ListTorrents("tv")
	if len(queued) != 1 || queued[0].Hash != wantID || queued[0].State != "queued" {
		t.Fatalf("queued jobs = %#v", queued)
	}

	// First poll creates the cloud folder, submits exactly once, and publishes
	// Premiumize's progress through the qBittorrent-shaped view.
	manager.PollOnce(context.Background())
	progress := manager.ListTorrents("tv")
	if len(progress) != 1 || progress[0].Progress != 0.45 || progress[0].State != "downloading" {
		t.Fatalf("progress after first poll = %#v, want downloading at 0.45", progress)
	}
	if createCount.Load() != 1 {
		t.Fatalf("transfer/create calls = %d, want 1", createCount.Load())
	}

	// Simulate process restart. The transfer ID and progress must be recovered
	// from disk, and the resumed manager must poll the existing transfer rather
	// than submit the source a second time.
	restarted, err := NewManager(&pm, cfg, configDir)
	if err != nil {
		t.Fatalf("reload job registry: %v", err)
	}
	restored := restarted.ListTorrents("tv")
	if len(restored) != 1 || restored[0].Hash != wantID || restored[0].Progress != 0.45 {
		t.Fatalf("restored job = %#v", restored)
	}
	restarted.mu.RLock()
	restoredJobPtr := restarted.jobs[wantID]
	if restoredJobPtr == nil {
		restarted.mu.RUnlock()
		t.Fatalf("restored registry is missing job %q", wantID)
	}
	restoredJob := *restoredJobPtr
	restarted.mu.RUnlock()
	if restoredJob.TransferID != transfer || restoredJob.CloudFolder != folder {
		t.Fatalf("restored Premiumize references = transfer %q, folder %q", restoredJob.TransferID, restoredJob.CloudFolder)
	}
	transferStatus.Store("finished")
	restarted.PollOnce(context.Background())
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		jobs := restarted.ListTorrents("tv")
		if len(jobs) == 1 && jobs[0].State == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	completed := restarted.ListTorrents("tv")
	if len(completed) != 1 || completed[0].State != "completed" || completed[0].Progress != 1 {
		t.Fatalf("completed jobs = %#v", completed)
	}
	if createCount.Load() != 1 {
		t.Fatalf("transfer/create calls after restart = %d, want exactly 1", createCount.Load())
	}
	got, err := os.ReadFile(filepath.Join(completed[0].ContentPath, "episode.mkv"))
	if err != nil {
		t.Fatalf("read imported output: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("downloaded content = %q, want %q", got, payload)
	}
	if strings.Contains(completed[0].ContentPath, ".partial") {
		t.Fatalf("published content path is still staged: %q", completed[0].ContentPath)
	}
}

func TestManagerQuotaExhaustionKeepsJobQueued(t *testing.T) {
	var createCalls atomic.Int32
	pm := managerTestPremiumize(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/account/info":
			fmt.Fprint(w, `{"status":"success","limit_used":1,"booster_points":0}`)
		case "/api/transfer/create":
			createCalls.Add(1)
			fmt.Fprint(w, `{"status":"success","id":"unexpected"}`)
		default:
			http.NotFound(w, r)
		}
	})
	manager := newTestManager(t, &pm, t.TempDir())
	if err := manager.AddMagnet(context.Background(), testMagnet, "tv"); err != nil {
		t.Fatal(err)
	}
	manager.PollOnce(context.Background())
	jobs := manager.ListTorrents("tv")
	if len(jobs) != 1 || jobs[0].State != "queued" {
		t.Fatalf("jobs after exhausted quota = %#v, want queued", jobs)
	}
	if createCalls.Load() != 0 {
		t.Fatalf("transfer/create calls = %d, want 0", createCalls.Load())
	}
}

func TestManagerFailedPremiumizeSubmissionStatus(t *testing.T) {
	var createCalls atomic.Int32
	pm := managerTestPremiumize(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/account/info":
			fmt.Fprint(w, `{"status":"success","limit_used":0,"booster_points":100}`)
		case "/api/folder/list":
			fmt.Fprint(w, `{"status":"success","content":[]}`)
		case "/api/folder/create":
			fmt.Fprint(w, `{"status":"success","id":"folder-1"}`)
		case "/api/transfer/create":
			createCalls.Add(1)
			fmt.Fprint(w, `{"status":"error","message":"invalid source"}`)
		case "/api/transfer/list":
			fmt.Fprint(w, `{"status":"success","transfers":[]}`)
		default:
			http.NotFound(w, r)
		}
	})
	manager := newTestManager(t, &pm, t.TempDir())
	if err := manager.AddMagnet(context.Background(), testMagnet, "tv"); err != nil {
		t.Fatal(err)
	}
	manager.PollOnce(context.Background())
	jobs := manager.ListTorrents("tv")
	if len(jobs) != 1 || jobs[0].State != "failed" || !strings.Contains(jobs[0].Error, "invalid source") {
		t.Fatalf("jobs after failed submission = %#v, want failed with Premiumize message", jobs)
	}
	if createCalls.Load() != 1 {
		t.Fatalf("transfer/create calls = %d, want 1", createCalls.Load())
	}
}

func TestManagerCompletedJobRemovalLocalDataPolicy(t *testing.T) {
	for _, tc := range []struct {
		name        string
		deleteFiles bool
		wantLocal   bool
		wantFolder  int32
	}{
		{name: "preserve local data", deleteFiles: false, wantLocal: true, wantFolder: 1},
		{name: "delete local data", deleteFiles: true, wantLocal: false, wantFolder: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var transferDeletes, folderDeletes atomic.Int32
			pm := managerTestPremiumize(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/transfer/delete":
					transferDeletes.Add(1)
					fmt.Fprint(w, `{"status":"success"}`)
				case "/api/folder/delete":
					folderDeletes.Add(1)
					fmt.Fprint(w, `{"status":"success"}`)
				default:
					http.NotFound(w, r)
				}
			})
			manager := newTestManager(t, &pm, t.TempDir())
			if err := manager.AddMagnet(context.Background(), testMagnet, "tv"); err != nil {
				t.Fatal(err)
			}
			const jobID = "0123456789abcdef0123456789abcdef01234567"
			manager.mu.Lock()
			job := manager.jobs[jobID]
			job.Phase, job.Progress = "completed", 1
			job.TransferID, job.CloudFolder = "transfer-1", "folder-1"
			if err := manager.saveLocked(); err != nil {
				manager.mu.Unlock()
				t.Fatal(err)
			}
			manager.mu.Unlock()
			localFile := filepath.Join(job.OutputPath, "episode.mkv")
			if err := os.MkdirAll(job.OutputPath, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(localFile, []byte("media"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := manager.RemoveTorrent(jobID, tc.deleteFiles); err != nil {
				t.Fatalf("remove completed job: %v", err)
			}
			if _, err := os.Stat(localFile); (err == nil) != tc.wantLocal {
				t.Fatalf("local data exists = %v, want %v (stat err %v)", err == nil, tc.wantLocal, err)
			}
			if transferDeletes.Load() != 1 {
				t.Errorf("transfer delete calls = %d, want 1", transferDeletes.Load())
			}
			if folderDeletes.Load() != tc.wantFolder {
				t.Errorf("folder delete calls = %d, want %d", folderDeletes.Load(), tc.wantFolder)
			}
			if len(manager.ListTorrents("tv")) != 0 {
				t.Errorf("removed job remains in torrent list")
			}
		})
	}
}

func TestManagerRestartMarksInterruptedSubmissionUnknown(t *testing.T) {
	pm := premiumizeme.NewPremiumizemeClient("test-key")
	configDir := t.TempDir()
	manager := newTestManager(t, &pm, configDir)
	if err := manager.AddMagnet(context.Background(), testMagnet, "tv"); err != nil {
		t.Fatal(err)
	}
	const jobID = "0123456789abcdef0123456789abcdef01234567"
	manager.mu.Lock()
	manager.jobs[jobID].Phase = "submitting"
	if err := manager.saveLocked(); err != nil {
		manager.mu.Unlock()
		t.Fatal(err)
	}
	manager.mu.Unlock()

	restarted, err := NewManager(&pm, &config.Config{TransferDirectory: "arrDownloads"}, configDir)
	if err != nil {
		t.Fatalf("restart manager: %v", err)
	}
	jobs := restarted.ListTorrents("tv")
	if len(jobs) != 1 || jobs[0].State != "failed" || !strings.Contains(jobs[0].Error, "outcome unknown") {
		t.Fatalf("job after interrupted submission restart = %#v, want failed with unknown outcome", jobs)
	}
}

const testMagnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Example.Release"

func managerTestPremiumize(t *testing.T, handler http.HandlerFunc) premiumizeme.Premiumizeme {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	oldTransport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	pm := premiumizeme.NewPremiumizemeClient("test-key")
	pm.APIBaseURL = server.URL + "/api/"
	pm.HTTPClient = server.Client()
	return pm
}

func newTestManager(t *testing.T, pm *premiumizeme.Premiumizeme, configDir string) *Manager {
	t.Helper()
	cfg := &config.Config{TransferDirectory: "arrDownloads", DownloadsDirectory: filepath.Join(t.TempDir(), "downloads"), SimultaneousDownloads: 1}
	if err := os.MkdirAll(cfg.DownloadsDirectory, 0755); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(pm, cfg, configDir)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}
