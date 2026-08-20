package httpapi

import (
	"context"
	"net/http"
	"strings"

	"video-production-console/internal/agentruntime/montageplan"
	"video-production-console/internal/domain"
	"video-production-console/internal/mediacatalog"
	"video-production-console/internal/partnerprofile"
	consoleSettings "video-production-console/internal/settings"
)

type partnerSetupSettings interface {
	Get(context.Context) (consoleSettings.View, error)
	Update(context.Context, domain.PublicSettings, map[string]string) (consoleSettings.View, error)
}

type PartnerSetupOptions struct {
	AppRoot  string
	DataRoot string
	Settings partnerSetupSettings
	Restart  func()
}

type PartnerSetupStatus struct {
	Complete             bool     `json:"complete"`
	DetectedJianyingRoot string   `json:"detected_jianying_root"`
	Codes                []string `json:"codes,omitempty"`
}

type partnerSetupRequest struct {
	JianyingRoot string `json:"jianying_root"`
	MediaRoot    string `json:"media_root"`
}

type partnerSetupHandler struct {
	appRoot  string
	dataRoot string
	settings partnerSetupSettings
	restart  func()
}

func NewPartnerSetupHandler(options PartnerSetupOptions) http.Handler {
	handler := &partnerSetupHandler{
		appRoot:  options.AppRoot,
		dataRoot: options.DataRoot,
		settings: options.Settings,
		restart:  options.Restart,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/partner/setup", handler.get)
	mux.HandleFunc("POST /api/partner/setup", handler.post)
	return mux
}

func (h *partnerSetupHandler) get(response http.ResponseWriter, request *http.Request) {
	status := PartnerSetupStatus{Codes: []string{}}
	if detected, err := partnerprofile.DetectJianyingRoot(); err == nil {
		status.DetectedJianyingRoot = detected
	}
	if h.settings != nil {
		if view, err := h.settings.Get(request.Context()); err == nil {
			status.Complete = consoleSettings.PartnerSetupComplete(view.Public)
		}
	}
	if !status.Complete {
		status.Codes = append(status.Codes, "setup_incomplete")
	}
	writeJSON(response, http.StatusOK, status)
}

func (h *partnerSetupHandler) post(response http.ResponseWriter, request *http.Request) {
	var input partnerSetupRequest
	if err := decodeJSON(response, request, maxSmallJSONRequest, &input); err != nil {
		writeDecodeError(response, err, "invalid_setup", "Jianying and scenery directories are required.")
		return
	}
	input.JianyingRoot = strings.TrimSpace(input.JianyingRoot)
	input.MediaRoot = strings.TrimSpace(input.MediaRoot)
	if input.JianyingRoot == "" || input.MediaRoot == "" {
		writeError(response, http.StatusBadRequest, "invalid_setup", "Jianying and scenery directories are required.")
		return
	}
	profile, err := partnerprofile.Build(partnerprofile.BuildOptions{
		AppRoot:      h.appRoot,
		DataRoot:     h.dataRoot,
		MediaRoot:    input.MediaRoot,
		JianyingRoot: input.JianyingRoot,
	})
	if err != nil {
		code := partnerprofile.CodeOf(err)
		if code == "" {
			code = "invalid_setup"
		}
		writeError(response, http.StatusBadRequest, code, "The selected directories could not be used.")
		return
	}
	if err := indexPartnerMedia(request.Context(), profile.MediaRoot); err != nil {
		writeError(response, http.StatusBadRequest, "media_index_failed", "The scenery library could not be indexed.")
		return
	}
	_, ffprobe := partnerprofile.FFmpegPaths(h.appRoot)
	if err := montageplan.WriteScenicMediaIndex(profile.MediaRoot, profile.MediaIndexPath, ffprobe); err != nil {
		writeError(response, http.StatusBadRequest, "media_index_failed", "The scenery library has no usable clips.")
		return
	}
	if err := montageplan.ValidateMediaLibrary(profile.MediaIndexPath, profile.MediaRoot, ""); err != nil {
		writeError(response, http.StatusBadRequest, "media_index_failed", "The scenery library has no usable clips.")
		return
	}
	if err := h.persistSetup(request.Context(), profile); err != nil {
		writeError(response, http.StatusInternalServerError, "settings_update_failed", "Setup could not be saved.")
		return
	}
	if h.restart != nil {
		go h.restart()
	}
	writeJSON(response, http.StatusAccepted, PartnerSetupStatus{Complete: true, Codes: []string{}})
}

func (h *partnerSetupHandler) persistSetup(ctx context.Context, profile partnerprofile.MachineProfile) error {
	if h.settings == nil {
		return nil
	}
	view, err := h.settings.Get(ctx)
	if err != nil {
		return err
	}
	public := view.Public
	if public.MaxCodexConcurrency < 1 {
		public.MaxCodexConcurrency = 2
	}
	public.MediaRoot = profile.MediaRoot
	public.JianyingRoot = profile.JianyingRoot
	public.MediaIndexPath = profile.MediaIndexPath
	public.MachineProfilePath = partnerprofile.ProfilePath(h.dataRoot)
	public.FFmpegPath, public.FFprobePath = mediacatalog.BundledPaths(h.appRoot)
	_, err = h.settings.Update(ctx, public, nil)
	return err
}

func indexPartnerMedia(ctx context.Context, mediaRoot string) error {
	repo, err := mediacatalog.Open(mediaRoot)
	if err != nil {
		return err
	}
	defer repo.Close()
	_, err = mediacatalog.NewIndexer(repo).Run(ctx)
	return err
}

