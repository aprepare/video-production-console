package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"video-production-console/internal/assets"
	"video-production-console/internal/domain"
	"video-production-console/internal/narration"
	consoleSettings "video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type narrationTestStore struct {
	mu       sync.Mutex
	project  domain.Project
	getErr   error
	assets   []domain.Asset
	listErr  error
	addErr   error
	addState store.CommitState
	added    []domain.Asset
	synced   int
	syncErr  error
}

func (s *narrationTestStore) GetProject(context.Context, string) (domain.Project, error) {
	return s.project, s.getErr
}

func (s *narrationTestStore) ListAssets(context.Context, string) ([]domain.Asset, error) {
	return s.assets, s.listErr
}

func (s *narrationTestStore) AddAsset(_ context.Context, asset *domain.Asset) (store.CommitState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.addErr != nil {
		return s.addState, s.addErr
	}
	s.added = append(s.added, *asset)
	return store.CommitCommitted, nil
}

func (s *narrationTestStore) SyncStageFromAssets(context.Context, string, time.Time) (domain.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.synced++
	return s.project, s.syncErr
}

func (s *narrationTestStore) addedTypes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	types := make([]string, 0, len(s.added))
	for _, asset := range s.added {
		types = append(types, string(asset.Type))
	}
	return types
}

type narrationTestSettings struct {
	runtime consoleSettings.Runtime
	err     error
}

func (s narrationTestSettings) Runtime(context.Context) (consoleSettings.Runtime, error) {
	return s.runtime, s.err
}

func configuredNarrationRuntime() consoleSettings.Runtime {
	return consoleSettings.Runtime{
		PublicSettings: domain.PublicSettings{
			VolcSpeechSpeakerID:  "ConsoleVoiceOne",
			VolcSpeechResourceID: "volc.megatts.default",
		},
		VolcSpeechAPIKey: "volc-key",
	}
}

// newNarrationTestHandler wires the handler over a real asset service so the
// generated files land on disk exactly as they do in production, with only the
// vendor call and the database replaced.
func newNarrationTestHandler(t *testing.T, repository *narrationTestStore, runtime AssetRuntimeProvider, produce func(context.Context, narration.ProduceRequest) (narration.Delivery, error)) (http.Handler, *assets.Service, string) {
	t.Helper()
	root := t.TempDir()
	service := assets.NewService(root)
	handler := newNarrationHandler(repository, service, NarrationHandlerOptions{Runtime: runtime, Produce: produce})
	return handler, service, root
}

// seedContinuousScript stores a script through the asset service and records it
// the way ListAssets would report it.
func seedContinuousScript(t *testing.T, service *assets.Service, repository *narrationTestStore, projectID, text string) {
	t.Helper()
	saved, err := service.SaveTextVersion(projectID, domain.AssetContinuousScript, "script.md", text)
	if err != nil {
		t.Fatalf("seed continuous script: %v", err)
	}
	id := projectID
	repository.assets = append(repository.assets, domain.Asset{
		ID: uuid.NewString(), ProjectID: &id, Type: domain.AssetContinuousScript,
		Version: 1, Path: saved.Path, Filename: "script.md", MIMEType: saved.MIMEType,
		Size: saved.Size, SHA256: saved.SHA256, Status: string(domain.AssetReady),
		CreatedAt: time.Now().UTC(),
	})
}

func seedSpokenScript(t *testing.T, service *assets.Service, repository *narrationTestStore, projectID, text string) {
	t.Helper()
	saved, err := service.SaveTextVersion(projectID, domain.AssetSpokenScript, "spoken_script.txt", text)
	if err != nil {
		t.Fatalf("seed spoken script: %v", err)
	}
	id := projectID
	repository.assets = append(repository.assets, domain.Asset{
		ID: uuid.NewString(), ProjectID: &id, Type: domain.AssetSpokenScript,
		Version: 1, Path: saved.Path, Filename: "spoken_script.txt", MIMEType: saved.MIMEType,
		Size: saved.Size, SHA256: saved.SHA256, Status: string(domain.AssetReady),
		CreatedAt: time.Now().UTC(),
	})
}

func seedNarrationInputs(t *testing.T, service *assets.Service, repository *narrationTestStore, projectID, continuous, spoken string) {
	t.Helper()
	seedContinuousScript(t, service, repository, projectID, continuous)
	if spoken == "" {
		spoken = continuous
	}
	seedSpokenScript(t, service, repository, projectID, spoken)
}

func narrationDelivery(provider ...string) narration.Delivery {
	providerName := "volc"
	if len(provider) > 0 {
		providerName = provider[0]
	}
	words := []narration.Word{{Text: "hello", StartTime: 0, EndTime: 2, Confidence: 1}}
	timing, _ := narration.NewWordTimingDocument("hello", providerName, "", words)
	return narration.Delivery{Audio: validProjectMP3(), AudioFormat: "mp3", SRT: "1\n00:00:00,000 --> 00:00:02,000\nhello\n", Captions: []narration.Caption{{Start: 0, End: 2, Text: "hello"}}, SpokenScript: "hello", Report: narration.QCReport{Pass: true, TextCoverage: 1, Warnings: []string{"subtitle timing advisory"}}, BilledWords: 3, Duration: 2, Script: "hello", Words: words, TimingDocument: timing}
}

func TestNarrationRejectsInvalidWordTimingBeforeRegisteringAssets(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*narration.Delivery)
	}{
		{"invalid interval", func(d *narration.Delivery) { d.Words[0].EndTime = d.Words[0].StartTime }},
		{"script hash mismatch", func(d *narration.Delivery) { d.TimingDocument.ScriptHash = "sha256:bad" }},
		{"document script mismatch", func(d *narration.Delivery) { d.TimingDocument.Script = "different" }},
		{"document words mismatch", func(d *narration.Delivery) { d.TimingDocument.Words[0].Text = "different" }},
		{"document provider mismatch", func(d *narration.Delivery) { d.TimingDocument.Provider = "other" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projectID := uuid.NewString()
			repository := &narrationTestStore{project: domain.Project{ID: projectID, Stage: domain.StageAssets}}
			handler, service, root := newNarrationTestHandler(t, repository, narrationTestSettings{runtime: configuredNarrationRuntime()}, func(_ context.Context, _ narration.ProduceRequest) (narration.Delivery, error) {
				d := narrationDelivery("volc")
				tc.mutate(&d)
				return d, nil
			})
			seedNarrationInputs(t, service, repository, projectID, "hello", "hello")
			response := performJSON(t, handler, http.MethodPost, "/api/projects/"+projectID+"/narration", nil)
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadGateway {
				t.Fatalf("status = %d, body = %s", response.StatusCode, readResponseBody(t, response))
			}
			var body struct {
				Code string `json:"code"`
			}
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Code != "narration_timing_invalid" {
				t.Fatalf("code = %q", body.Code)
			}
			if got := repository.addedTypes(); len(got) != 0 {
				t.Fatalf("added assets = %v", got)
			}
			if _, err := os.Stat(filepath.Join(root, "projects", projectID, "narration")); !os.IsNotExist(err) {
				t.Fatalf("narration directory exists or stat failed: %v", err)
			}
		})
	}
}

func TestNarrationStoresAudioAndSubtitleFromTheContinuousScript(t *testing.T) {
	projectID := uuid.NewString()
	repository := &narrationTestStore{project: domain.Project{ID: projectID, Stage: domain.StageAssets}}
	var got narration.ProduceRequest
	handler, service, root := newNarrationTestHandler(t, repository,
		narrationTestSettings{runtime: configuredNarrationRuntime()},
		func(_ context.Context, request narration.ProduceRequest) (narration.Delivery, error) {
			got = request
			return narrationDelivery(request.Provider), nil
		})
	seedNarrationInputs(t, service, repository, projectID, "# 标题\n\n第一句。", "第一句")

	response := performJSON(t, handler, http.MethodPost, "/api/projects/"+projectID+"/narration", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.StatusCode, readResponseBody(t, response))
	}
	var view struct {
		Narration        assetView `json:"narration"`
		SubtitleSRT      assetView `json:"subtitle_srt"`
		SpokenScript     assetView `json:"spoken_script"`
		WordTiming       assetView `json:"word_timing"`
		Captions         int       `json:"captions"`
		DurationSeconds  float64   `json:"duration_seconds"`
		BilledCharacters int       `json:"billed_characters"`
		Warnings         []string  `json:"warnings"`
	}
	if err := json.NewDecoder(response.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	if got.Script != "# 标题\n\n第一句。" || got.SpeakerID != "ConsoleVoiceOne" || got.Provider != "volc" {
		t.Errorf("produce request = %+v, want the continuous script as TTS input and configured voice", got)
	}
	if len(got.SpokenLines) != 1 || got.SpokenLines[0] != "第一句" {
		t.Errorf("spoken lines = %v, want the 口播稿 lines for subtitle cuts", got.SpokenLines)
	}
	if view.Narration.Filename != "narration.mp3" || view.SubtitleSRT.Filename != "narration.srt" || view.SpokenScript.Filename != "spoken_script.txt" || view.WordTiming.Filename == "" {
		t.Errorf("view = %+v, want audio, SRT, and spoken lines named", view)
	}
	if view.Captions != 1 || view.DurationSeconds != 2 || view.BilledCharacters != 3 {
		t.Errorf("view = %+v, want the delivery measurements reported", view)
	}
	if len(view.Warnings) != 1 {
		t.Errorf("warnings = %v, want the gate's advisory passed through", view.Warnings)
	}
	if types := repository.addedTypes(); len(types) != 3 || types[0] != "narration" || types[1] != "subtitle_srt" || types[2] != "word_timing" {
		t.Errorf("registered asset types = %v, want narration, subtitle, then word timing", types)
	}
	// The stage gate reads assets from the database, so a narration that is not
	// followed by a stage sync leaves the project stuck at the asset stage.
	if repository.synced != 1 {
		t.Errorf("stage syncs = %d, want exactly one after a successful run", repository.synced)
	}
	for _, dir := range []string{"narration", "subtitle_srt", "spoken_script", "word_timing"} {
		entries, err := os.ReadDir(filepath.Join(root, "projects", projectID, dir))
		if err != nil || len(entries) != 1 {
			t.Errorf("%s entries = %v, err = %v", dir, entries, err)
		}
	}
	// The API exposes the logical filename, while managed assets are persisted
	// under UUID-backed paths. Read the registered asset path and keep the
	// response filename assertion above focused on its public contract.
	var wordTimingPath string
	for _, asset := range repository.added {
		if asset.Type == domain.AssetWordTiming {
			wordTimingPath = asset.Path
			break
		}
	}
	if wordTimingPath == "" {
		t.Fatal("word timing asset was not registered")
	}
	b, err := os.ReadFile(wordTimingPath)
	if err != nil {
		t.Fatal(err)
	}
	var timing struct {
		SchemaVersion int               `json:"schema_version"`
		Words         []json.RawMessage `json:"words"`
		ScriptHash    string            `json:"script_hash"`
	}
	if err := json.Unmarshal(b, &timing); err != nil {
		t.Fatalf("word timing JSON: %v", err)
	}
	if timing.SchemaVersion == 0 || len(timing.Words) == 0 || timing.ScriptHash == "" {
		t.Fatalf("invalid word timing envelope: %+v", timing)
	}
}

// The script is the only input, so its absence is an ordering problem the
// operator can fix rather than a server fault.
func TestNarrationRequiresAReadyContinuousScript(t *testing.T) {
	projectID := uuid.NewString()
	for _, tt := range []struct {
		name     string
		mutate   func(*narrationTestStore, *assets.Service)
		wantCode string
	}{
		{"no assets", func(*narrationTestStore, *assets.Service) {}, "continuous_script_missing"},
		{"script not ready", func(s *narrationTestStore, service *assets.Service) {
			saved, err := service.SaveTextVersion(projectID, domain.AssetContinuousScript, "script.md", "文案")
			if err != nil {
				t.Fatal(err)
			}
			id := projectID
			s.assets = []domain.Asset{{ID: uuid.NewString(), ProjectID: &id, Type: domain.AssetContinuousScript, Path: saved.Path, Status: "stale"}}
		}, "continuous_script_missing"},
		{"script file deleted", func(s *narrationTestStore, service *assets.Service) {
			id := projectID
			s.assets = []domain.Asset{{ID: uuid.NewString(), ProjectID: &id, Type: domain.AssetContinuousScript, Path: filepath.Join("projects", projectID, "continuous_script", "gone.md"), Status: string(domain.AssetReady)}}
		}, "continuous_script_missing"},
		{"assets unreadable", func(s *narrationTestStore, _ *assets.Service) {
			s.listErr = errors.New("database unavailable")
		}, "project_assets_failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repository := &narrationTestStore{project: domain.Project{ID: projectID, Stage: domain.StageAssets}}
			handler, service, _ := newNarrationTestHandler(t, repository,
				narrationTestSettings{runtime: configuredNarrationRuntime()},
				func(context.Context, narration.ProduceRequest) (narration.Delivery, error) {
					t.Error("synthesis must not be billed without a script")
					return narration.Delivery{}, nil
				})
			tt.mutate(repository, service)
			response := performJSON(t, handler, http.MethodPost, "/api/projects/"+projectID+"/narration", nil)
			assertAPIError(t, response, http.StatusConflict, tt.wantCode)
		})
	}
}

func TestNarrationRequiresAReadySpokenScript(t *testing.T) {
	projectID := uuid.NewString()
	repository := &narrationTestStore{project: domain.Project{ID: projectID, Stage: domain.StageAssets}}
	handler, service, _ := newNarrationTestHandler(t, repository,
		narrationTestSettings{runtime: configuredNarrationRuntime()},
		func(context.Context, narration.ProduceRequest) (narration.Delivery, error) {
			t.Error("synthesis must not be billed without a spoken script")
			return narration.Delivery{}, nil
		})
	seedContinuousScript(t, service, repository, projectID, "文案")
	response := performJSON(t, handler, http.MethodPost, "/api/projects/"+projectID+"/narration", nil)
	assertAPIError(t, response, http.StatusConflict, "spoken_script_missing")
}

func TestNarrationUsesAuraSTDVoiceSettingsWhenConfigured(t *testing.T) {
	projectID := uuid.NewString()
	repository := &narrationTestStore{project: domain.Project{ID: projectID, Stage: domain.StageAssets}}
	var got narration.ProduceRequest
	handler, service, _ := newNarrationTestHandler(t, repository,
		narrationTestSettings{runtime: consoleSettings.Runtime{
			PublicSettings: domain.PublicSettings{
				TTSProvider:            "aurastd",
				AuraSTDModel:           "speech-2.8-hd",
				AuraSTDVoiceID:         "moss_audio_6b1797c8-2329-11f1-8c29-36c83b29da67",
				AuraSTDSpeed:           1.21,
				AuraSTDVolume:          1.4,
				AuraSTDPitch:           1,
				AuraSTDModifyIntensity: 5,
				AuraSTDModifyTimbre:    6,
				AuraSTDLanguageBoost:   "Chinese",
			},
			AuraSTDTTsAPIKey: "aurastd-key",
		}},
		func(_ context.Context, request narration.ProduceRequest) (narration.Delivery, error) {
			got = request
			return narrationDelivery(request.Provider), nil
		})
	seedNarrationInputs(t, service, repository, projectID, "货还在。", "货还在")

	response := performJSON(t, handler, http.MethodPost, "/api/projects/"+projectID+"/narration", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.StatusCode, readResponseBody(t, response))
	}
	if got.Provider != "aurastd" || got.SpeakerID != "moss_audio_6b1797c8-2329-11f1-8c29-36c83b29da67" {
		t.Errorf("provider/voice = %+v", got)
	}
	if got.Speed != 1.21 || got.Volume != 1.4 || got.Pitch != 1 || got.ModifyIntensity != 5 || got.ModifyTimbre != 6 {
		t.Errorf("voice params = %+v", got)
	}
}

func TestNarrationReportsMissingVendorConfiguration(t *testing.T) {
	projectID := uuid.NewString()
	for _, tt := range []struct {
		name     string
		provider AssetRuntimeProvider
	}{
		{"no provider", nil},
		{"unreadable settings", narrationTestSettings{err: errors.New("settings unavailable")}},
		{"missing key", narrationTestSettings{runtime: consoleSettings.Runtime{PublicSettings: domain.PublicSettings{VolcSpeechSpeakerID: "ConsoleVoiceOne"}}}},
		{"missing voice", narrationTestSettings{runtime: consoleSettings.Runtime{VolcSpeechAPIKey: "volc-key"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repository := &narrationTestStore{project: domain.Project{ID: projectID, Stage: domain.StageAssets}}
			handler, service, _ := newNarrationTestHandler(t, repository, tt.provider,
				func(context.Context, narration.ProduceRequest) (narration.Delivery, error) {
					t.Error("synthesis must not run without credentials")
					return narration.Delivery{}, nil
				})
			seedNarrationInputs(t, service, repository, projectID, "文案", "文案")
			response := performJSON(t, handler, http.MethodPost, "/api/projects/"+projectID+"/narration", nil)
			assertAPIError(t, response, http.StatusServiceUnavailable, "narration_not_configured")
		})
	}
}

// A failed gate is a script problem, and the response has to name the failing
// cue so the operator knows what to edit instead of retrying blindly.
func TestNarrationSurfacesQualityGateFailuresWithoutStoringFiles(t *testing.T) {
	projectID := uuid.NewString()
	repository := &narrationTestStore{project: domain.Project{ID: projectID, Stage: domain.StageAssets}}
	handler, service, root := newNarrationTestHandler(t, repository,
		narrationTestSettings{runtime: configuredNarrationRuntime()},
		func(context.Context, narration.ProduceRequest) (narration.Delivery, error) {
			return narrationDelivery(), &narration.QualityGateError{Report: narration.QCReport{
				Failures: []string{"cue 3 lasts 0.20s, below the 0.50s floor"},
			}}
		})
	seedNarrationInputs(t, service, repository, projectID, "文案", "文案")

	response := performJSON(t, handler, http.MethodPost, "/api/projects/"+projectID+"/narration", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %s", response.StatusCode, readResponseBody(t, response))
	}
	var body struct{ Code, Message string }
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "narration_quality_gate" || !strings.Contains(body.Message, "0.50s floor") {
		t.Errorf("error = %+v, want the failing cue explained", body)
	}
	if len(repository.addedTypes()) != 0 {
		t.Error("a rejected narration must not be registered")
	}
	if _, err := os.Stat(filepath.Join(root, "projects", projectID, "narration")); !os.IsNotExist(err) {
		t.Errorf("narration directory should not exist, stat err = %v", err)
	}
}

func TestNarrationMapsVendorAndProjectFailures(t *testing.T) {
	projectID := uuid.NewString()
	t.Run("vendor failure", func(t *testing.T) {
		repository := &narrationTestStore{project: domain.Project{ID: projectID, Stage: domain.StageAssets}}
		handler, service, _ := newNarrationTestHandler(t, repository,
			narrationTestSettings{runtime: configuredNarrationRuntime()},
			func(context.Context, narration.ProduceRequest) (narration.Delivery, error) {
				return narration.Delivery{}, errors.New("code 45000000: speaker permission denied")
			})
		seedNarrationInputs(t, service, repository, projectID, "文案", "文案")
		response := performJSON(t, handler, http.MethodPost, "/api/projects/"+projectID+"/narration", nil)
		assertAPIError(t, response, http.StatusBadGateway, "narration_synthesis_failed")
	})
	t.Run("project missing", func(t *testing.T) {
		repository := &narrationTestStore{getErr: store.ErrProjectNotFound}
		handler, _, _ := newNarrationTestHandler(t, repository,
			narrationTestSettings{runtime: configuredNarrationRuntime()},
			func(context.Context, narration.ProduceRequest) (narration.Delivery, error) {
				t.Error("synthesis must not run for a missing project")
				return narration.Delivery{}, nil
			})
		response := performJSON(t, handler, http.MethodPost, "/api/projects/"+projectID+"/narration", nil)
		assertAPIError(t, response, http.StatusNotFound, "project_not_found")
	})
	t.Run("project unreadable", func(t *testing.T) {
		repository := &narrationTestStore{getErr: errors.New("database unavailable")}
		handler, _, _ := newNarrationTestHandler(t, repository,
			narrationTestSettings{runtime: configuredNarrationRuntime()}, nil)
		response := performJSON(t, handler, http.MethodPost, "/api/projects/"+projectID+"/narration", nil)
		assertAPIError(t, response, http.StatusInternalServerError, "project_read_failed")
	})
	t.Run("invalid project id", func(t *testing.T) {
		handler, _, _ := newNarrationTestHandler(t, &narrationTestStore{}, narrationTestSettings{runtime: configuredNarrationRuntime()}, nil)
		response := performJSON(t, handler, http.MethodPost, "/api/projects/not-a-uuid/narration", nil)
		if response.StatusCode == http.StatusCreated {
			t.Fatalf("status = %d, want a rejection", response.StatusCode)
		}
		response.Body.Close()
	})
}

// Synthesis is billed per character, so a second click while the first run is
// still streaming must be refused rather than doubling the cost.
func TestNarrationRefusesASecondRunWhileOneIsInFlight(t *testing.T) {
	projectID := uuid.NewString()
	repository := &narrationTestStore{project: domain.Project{ID: projectID, Stage: domain.StageAssets}}
	entered, release := make(chan struct{}), make(chan struct{})
	handler, service, _ := newNarrationTestHandler(t, repository,
		narrationTestSettings{runtime: configuredNarrationRuntime()},
		func(context.Context, narration.ProduceRequest) (narration.Delivery, error) {
			entered <- struct{}{}
			<-release
			return narrationDelivery(), nil
		})
	seedNarrationInputs(t, service, repository, projectID, "文案", "文案")

	done := make(chan int, 1)
	go func() {
		first := performJSON(t, handler, http.MethodPost, "/api/projects/"+projectID+"/narration", nil)
		first.Body.Close()
		done <- first.StatusCode
	}()
	<-entered
	second := performJSON(t, handler, http.MethodPost, "/api/projects/"+projectID+"/narration", nil)
	assertAPIError(t, second, http.StatusConflict, "narration_in_progress")
	close(release)
	if status := <-done; status != http.StatusCreated {
		t.Fatalf("first run status = %d", status)
	}
}

// The audio is what the synthesis was billed for, so it stays registered even
// when the subtitle cannot be recorded; the subtitle is rebuildable, the audio
// is not free.
func TestNarrationKeepsTheAudioWhenSubtitleRegistrationFails(t *testing.T) {
	projectID := uuid.NewString()
	repository := &narrationTestStore{project: domain.Project{ID: projectID, Stage: domain.StageAssets}}
	root := t.TempDir()
	service := assets.NewService(root)
	seedNarrationInputs(t, service, repository, projectID, "文案", "文案")
	handler := newNarrationHandler(&narrationSubtitleFailure{store: repository}, service, NarrationHandlerOptions{
		Runtime: narrationTestSettings{runtime: configuredNarrationRuntime()},
		Produce: func(context.Context, narration.ProduceRequest) (narration.Delivery, error) {
			return narrationDelivery(), nil
		},
	})

	response := performJSON(t, handler, http.MethodPost, "/api/projects/"+projectID+"/narration", nil)
	assertAPIError(t, response, http.StatusServiceUnavailable, "asset_commit_unknown")
	entries, err := os.ReadDir(filepath.Join(root, "projects", projectID, "narration"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("narration entries = %v, err = %v", entries, err)
	}
	if types := repository.addedTypes(); len(types) != 1 || types[0] != "narration" {
		t.Errorf("registered types = %v, want the narration kept", types)
	}
}

// narrationSubtitleFailure accepts the narration and then reports an unknown
// commit outcome for the subtitle, the case where the file must not be deleted.
type narrationSubtitleFailure struct {
	store *narrationTestStore
	calls int
}

func (s *narrationSubtitleFailure) GetProject(ctx context.Context, id string) (domain.Project, error) {
	return s.store.GetProject(ctx, id)
}

func (s *narrationSubtitleFailure) ListAssets(ctx context.Context, id string) ([]domain.Asset, error) {
	return s.store.ListAssets(ctx, id)
}

func (s *narrationSubtitleFailure) AddAsset(ctx context.Context, asset *domain.Asset) (store.CommitState, error) {
	s.calls++
	if s.calls > 1 {
		return store.CommitUnknown, &store.CommitOutcomeError{Outcome: store.CommitUnknown, Err: errors.New("lost commit acknowledgement")}
	}
	return s.store.AddAsset(ctx, asset)
}
