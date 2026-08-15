package catalogbuilder

import "errors"

var (
	errInvalidConfig  = errors.New("catalog_builder_invalid_config")
	errInvalidPack    = errors.New("catalog_builder_invalid_pack")
	errModelMismatch  = errors.New("catalog_builder_model_mismatch")
	errJobActive      = errors.New("catalog_builder_job_active")
	errPackNotReady   = errors.New("catalog_builder_pack_not_ready")
	ErrListenNotLocal = errors.New("catalog_builder_listen_not_loopback")
	errBuildFailed    = errors.New("catalog_builder_build_failed")
	errMergeFailed    = errors.New("catalog_builder_merge_failed")
	errVisionRequired = errors.New("catalog_builder_vision_required")
	errFFmpegRequired = errors.New("catalog_builder_ffmpeg_required")
	errNoMedia        = errors.New("catalog_builder_no_media")
)
