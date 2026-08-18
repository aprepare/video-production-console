// Package mediacatalog owns the standalone media intelligence catalog stored
// in media_root/catalog.db. It only records relative paths; every filesystem
// access re-validates containment against the canonical media root.
package mediacatalog

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type SourceKind string

const (
	SourceKindMovie SourceKind = "movie"
	SourceKindBroll SourceKind = "broll"
	SourceKindImage SourceKind = "image"
)

type SourceSubtype string

const (
	SourceSubtypeVideo        SourceSubtype = "video"
	SourceSubtypePhoto        SourceSubtype = "photo"
	SourceSubtypeChart        SourceSubtype = "chart"
	SourceSubtypeIllustration SourceSubtype = "illustration"
	SourceSubtypeGenerated    SourceSubtype = "generated"
)

type SourceOrigin string

const (
	SourceOriginLocal     SourceOrigin = "local"
	SourceOriginGenerated SourceOrigin = "generated"
	SourceOriginPexels    SourceOrigin = "pexels"
	SourceOriginPixabay   SourceOrigin = "pixabay"
)

type SourceStatus string

const (
	SourceStatusPendingProbe SourceStatus = "pending_probe"
	SourceStatusReady        SourceStatus = "ready"
	SourceStatusFailed       SourceStatus = "failed"
)

type AnalysisStatus string

const (
	AnalysisPending   AnalysisStatus = "pending"
	AnalysisCompleted AnalysisStatus = "completed"
	AnalysisFailed    AnalysisStatus = "failed"
)

type JobStatus string

const (
	JobPending   JobStatus = "pending"
	JobRunning   JobStatus = "running"
	JobCompleted JobStatus = "completed"
	JobFailed    JobStatus = "failed"
)

var (
	ErrInvalidValue       = errors.New("media catalog value is invalid")
	ErrSourceNotFound     = errors.New("media source not found")
	ErrShotNotFound       = errors.New("media shot not found")
	ErrJobNotFound        = errors.New("media job not found")
	ErrRightsNotFound     = errors.New("media rights not found")
	ErrUnsafeRelativePath = errors.New("relative path escapes the media root")
)

var catalogSHA256Pattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

type Source struct {
	ID           string
	Kind         SourceKind
	Subtype      SourceSubtype
	Origin       SourceOrigin
	RelativePath string
	SHA256       string
	SizeBytes    int64
	MIMEType     string
	Width        int
	Height       int
	DurationMS   int64
	FPS          float64
	Status       SourceStatus
	ErrorCode    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (s Source) Validate() error {
	switch s.Kind {
	case SourceKindMovie, SourceKindBroll, SourceKindImage:
	default:
		return fmt.Errorf("%w: kind %q", ErrInvalidValue, s.Kind)
	}
	switch s.Subtype {
	case SourceSubtypeVideo, SourceSubtypePhoto, SourceSubtypeChart, SourceSubtypeIllustration, SourceSubtypeGenerated:
	default:
		return fmt.Errorf("%w: subtype %q", ErrInvalidValue, s.Subtype)
	}
	if s.Kind == SourceKindImage {
		if s.Subtype == SourceSubtypeVideo {
			return fmt.Errorf("%w: image sources cannot use the video subtype", ErrInvalidValue)
		}
	} else if s.Subtype != SourceSubtypeVideo {
		return fmt.Errorf("%w: %s sources must use the video subtype", ErrInvalidValue, s.Kind)
	}
	switch s.Origin {
	case SourceOriginLocal, SourceOriginGenerated, SourceOriginPexels, SourceOriginPixabay:
	default:
		return fmt.Errorf("%w: origin %q", ErrInvalidValue, s.Origin)
	}
	switch s.Status {
	case SourceStatusPendingProbe, SourceStatusReady, SourceStatusFailed:
	default:
		return fmt.Errorf("%w: status %q", ErrInvalidValue, s.Status)
	}
	if err := ValidateRelativePath(s.RelativePath); err != nil {
		return err
	}
	if !catalogSHA256Pattern.MatchString(s.SHA256) {
		return fmt.Errorf("%w: sha256 must be 64 hexadecimal characters", ErrInvalidValue)
	}
	if s.SizeBytes < 0 || s.DurationMS < 0 || s.Width < 0 || s.Height < 0 || s.FPS < 0 {
		return fmt.Errorf("%w: source measurements cannot be negative", ErrInvalidValue)
	}
	return nil
}

type Shot struct {
	ID              string
	SourceID        string
	Ordinal         int
	SourceInMS      int64
	SourceOutMS     int64
	DurationMS      int64
	AnalysisStatus  AnalysisStatus
	Summary         string
	Mood            string
	Setting         string
	PeopleCount     int
	MotionLevel     string
	HasText         bool
	AnalysisVersion string
}

func (s Shot) validateBasics() error {
	if strings.TrimSpace(s.SourceID) == "" {
		return fmt.Errorf("%w: shot source is required", ErrInvalidValue)
	}
	if s.Ordinal < 0 {
		return fmt.Errorf("%w: shot ordinal cannot be negative", ErrInvalidValue)
	}
	switch s.AnalysisStatus {
	case "", AnalysisPending, AnalysisCompleted, AnalysisFailed:
	default:
		return fmt.Errorf("%w: analysis status %q", ErrInvalidValue, s.AnalysisStatus)
	}
	return nil
}

// Embedding is a shot vector with the metadata that keeps it reproducible.
type Embedding struct {
	Model           string
	Dimension       int
	Vector          []float32
	AnalysisVersion string
}

type Keyframe struct {
	ID           string
	ShotID       string
	Ordinal      int
	RelativePath string
	AtMS         int64
	Width        int
	Height       int
	SHA256       string
}

func (k Keyframe) Validate() error {
	if strings.TrimSpace(k.ShotID) == "" {
		return fmt.Errorf("%w: keyframe shot is required", ErrInvalidValue)
	}
	if k.Ordinal < 0 || k.AtMS < 0 || k.Width < 0 || k.Height < 0 {
		return fmt.Errorf("%w: keyframe measurements cannot be negative", ErrInvalidValue)
	}
	if err := ValidateRelativePath(k.RelativePath); err != nil {
		return err
	}
	if !catalogSHA256Pattern.MatchString(k.SHA256) {
		return fmt.Errorf("%w: keyframe sha256 must be 64 hexadecimal characters", ErrInvalidValue)
	}
	return nil
}

type Tag struct {
	ShotID     string
	Namespace  string
	Value      string
	Confidence float64
}

func (t Tag) Validate() error {
	if strings.TrimSpace(t.Namespace) == "" || strings.TrimSpace(t.Value) == "" {
		return fmt.Errorf("%w: tag namespace and value are required", ErrInvalidValue)
	}
	if t.Confidence < 0 || t.Confidence > 1 {
		return fmt.Errorf("%w: tag confidence must stay within [0,1]", ErrInvalidValue)
	}
	return nil
}

type Rights struct {
	SourceID    string
	SourceURL   string
	Creator     string
	LicenseCode string
	LicenseURL  string
	Attribution string
	RetrievedAt time.Time
	RightsNotes string
}

func (r Rights) Validate() error {
	if strings.TrimSpace(r.SourceID) == "" {
		return fmt.Errorf("%w: rights source is required", ErrInvalidValue)
	}
	return nil
}

type Job struct {
	ID             string
	SourceID       string
	Phase          string
	Status         JobStatus
	CompletedUnits int
	TotalUnits     int
	ErrorCode      string
	ErrorMessage   string
	StartedAt      *time.Time
	FinishedAt     *time.Time
}

// ValidateRelativePath accepts forward-slash relative paths that stay strictly
// inside the media root: no absolute paths, drive letters, empty, "." or ".."
// segments.
func ValidateRelativePath(value string) error {
	if value == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("%w: relative path is required", ErrUnsafeRelativePath)
	}
	if filepath.IsAbs(value) || filepath.VolumeName(value) != "" ||
		strings.HasPrefix(value, "/") || strings.HasPrefix(value, "\\") {
		return fmt.Errorf("%w: %q is absolute", ErrUnsafeRelativePath, value)
	}
	normalized := strings.ReplaceAll(value, "\\", "/")
	for _, part := range strings.Split(normalized, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("%w: %q contains unsafe segments", ErrUnsafeRelativePath, value)
		}
	}
	return nil
}
