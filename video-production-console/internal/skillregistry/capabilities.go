package skillregistry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"video-production-console/internal/domain"
)

const (
	CapabilitiesRelativePath = "assets/capabilities.json"
	knownCapabilityContract  = "1.0"
	planVersionV1            = "1.0"
	planVersionV2            = "2.0"
)

// Capabilities is the frozen skill declaration that gates production-plan v2.
// Presence of a file is not enough: the snapshot must list it, the on-disk
// bytes must match the frozen SHA-256, and the contract must be known.
type Capabilities struct {
	ContractVersion        string   `json:"contract_version"`
	ProductionPlanVersions []string `json:"production_plan_versions"`
	Features               []string `json:"features"`
}

// PlanVersionDecision is the safe default for a snapshot: v1 unless v2 is
// explicitly and validly declared. Parse failures never start v2.
type PlanVersionDecision struct {
	Version string
	Warning string
}

func DecideMontagePlanVersion(snapshot domain.SkillSnapshot) PlanVersionDecision {
	capabilities, err := ParseSnapshotCapabilities(snapshot)
	if err != nil {
		if warning, ok := capabilityWarning(err); ok {
			return PlanVersionDecision{Version: planVersionV1, Warning: warning}
		}
		return PlanVersionDecision{Version: planVersionV1}
	}
	if capabilities.ContractVersion != knownCapabilityContract {
		return PlanVersionDecision{Version: planVersionV1, Warning: "capability_unknown"}
	}
	if !containsVersion(capabilities.ProductionPlanVersions, planVersionV2) {
		return PlanVersionDecision{Version: planVersionV1}
	}
	return PlanVersionDecision{Version: planVersionV2}
}

func ParseSnapshotCapabilities(snapshot domain.SkillSnapshot) (Capabilities, error) {
	entry, ok := findSnapshotFile(snapshot, CapabilitiesRelativePath)
	if !ok {
		return Capabilities{}, errCapabilityMissing
	}
	root := skillRoot(snapshot)
	if root == "" {
		return Capabilities{}, fmt.Errorf("%w: skill root is empty", errCapabilityMissing)
	}
	path := filepath.Join(root, filepath.FromSlash(CapabilitiesRelativePath))
	payload, err := os.ReadFile(path)
	if err != nil {
		return Capabilities{}, fmt.Errorf("%w: %v", errCapabilityMissing, err)
	}
	sum := sha256.Sum256(payload)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), strings.TrimSpace(entry.SHA256)) {
		return Capabilities{}, errCapabilityHashMismatch
	}
	var capabilities Capabilities
	if err := json.Unmarshal(payload, &capabilities); err != nil {
		return Capabilities{}, fmt.Errorf("%w: %v", errCapabilityMalformed, err)
	}
	capabilities.ContractVersion = strings.TrimSpace(capabilities.ContractVersion)
	return capabilities, nil
}

var (
	errCapabilityMissing      = errors.New("capability_missing")
	errCapabilityMalformed    = errors.New("capability_parse_failed")
	errCapabilityHashMismatch = errors.New("capability_hash_mismatch")
)

func capabilityWarning(err error) (string, bool) {
	switch {
	case err == nil, errors.Is(err, errCapabilityMissing):
		return "", false
	case errors.Is(err, errCapabilityMalformed):
		return "capability_parse_failed", true
	case errors.Is(err, errCapabilityHashMismatch):
		return "capability_hash_mismatch", true
	default:
		return "capability_parse_failed", true
	}
}

func findSnapshotFile(snapshot domain.SkillSnapshot, relative string) (domain.SkillFileSnapshot, bool) {
	want := filepath.ToSlash(strings.TrimSpace(relative))
	for _, file := range snapshot.Files {
		if filepath.ToSlash(strings.TrimSpace(file.Path)) == want {
			return file, true
		}
	}
	return domain.SkillFileSnapshot{}, false
}

func skillRoot(snapshot domain.SkillSnapshot) string {
	path := strings.TrimSpace(snapshot.Path)
	if path == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err == nil && !info.IsDir() {
		return filepath.Dir(path)
	}
	return path
}

func containsVersion(versions []string, want string) bool {
	for _, version := range versions {
		if strings.TrimSpace(version) == want {
			return true
		}
	}
	return false
}
