package skillregistry

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"video-production-console/internal/domain"
)

func writeFrozenCapabilities(t *testing.T, root string, payload []byte) domain.SkillSnapshot {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(CapabilitiesRelativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	return domain.SkillSnapshot{
		ID:   "snap-cap",
		Name: "jianying-montage-draft",
		Path: root,
		Files: []domain.SkillFileSnapshot{{
			Path:   CapabilitiesRelativePath,
			SHA256: hex.EncodeToString(sum[:]),
			Size:   int64(len(payload)),
		}},
	}
}

func TestDecideMontagePlanVersionRequiresFrozenV2Declaration(t *testing.T) {
	snapshot := writeFrozenCapabilities(t, t.TempDir(), []byte(`{
  "contract_version": "1.0",
  "production_plan_versions": ["1.0", "2.0"],
  "features": ["highlight_captions", "image_keyframes", "style_policy_v2"]
}`))
	decision := DecideMontagePlanVersion(snapshot)
	if decision.Version != "2.0" || decision.Warning != "" {
		t.Fatalf("decision = %+v, want version 2.0 without warning", decision)
	}
}

func TestDecideMontagePlanVersionMissingFileStaysV1(t *testing.T) {
	decision := DecideMontagePlanVersion(domain.SkillSnapshot{
		Path: t.TempDir(),
		Files: []domain.SkillFileSnapshot{{
			Path: "SKILL.md", SHA256: "abc", Size: 1,
		}},
	})
	if decision.Version != "1.0" || decision.Warning != "" {
		t.Fatalf("missing capability must stay v1 silently; got %+v", decision)
	}
}

func TestDecideMontagePlanVersionMalformedStaysV1WithWarning(t *testing.T) {
	snapshot := writeFrozenCapabilities(t, t.TempDir(), []byte(`{"contract_version":`))
	decision := DecideMontagePlanVersion(snapshot)
	if decision.Version != "1.0" || decision.Warning == "" {
		t.Fatalf("malformed capability must stay v1 with warning; got %+v", decision)
	}
}

func TestDecideMontagePlanVersionUnknownContractDoesNotEnableV2(t *testing.T) {
	snapshot := writeFrozenCapabilities(t, t.TempDir(), []byte(`{
  "contract_version": "9.0",
  "production_plan_versions": ["2.0"],
  "features": ["highlight_captions"]
}`))
	decision := DecideMontagePlanVersion(snapshot)
	if decision.Version != "1.0" {
		t.Fatalf("unknown contract must not enable v2; got %+v", decision)
	}
}

func TestDecideMontagePlanVersionHashMismatchStaysV1(t *testing.T) {
	root := t.TempDir()
	snapshot := writeFrozenCapabilities(t, root, []byte(`{
  "contract_version": "1.0",
  "production_plan_versions": ["1.0", "2.0"],
  "features": ["highlight_captions"]
}`))
	snapshot.Files[0].SHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	decision := DecideMontagePlanVersion(snapshot)
	if decision.Version != "1.0" || decision.Warning == "" {
		t.Fatalf("hash mismatch must stay v1 with warning; got %+v", decision)
	}
}
