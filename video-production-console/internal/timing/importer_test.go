package timing

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSkillTimingImportValidatesIdentityBoundsDurationAndSHA(t *testing.T) {
	t0 := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	expectation := ImportExpectation{TaskID: "task-1", SkillSnapshotID: "snapshot-1", Attempt: 2, NotBefore: t0, NotAfter: t0.Add(time.Minute)}
	valid := fmt.Sprintf(`{"schema_version":"1.0","task_id":"task-1","skill_snapshot_id":"snapshot-1","attempt":2,"phases":[{"phase_key":"draft_build","display_name":"草稿构建","source":"skill","state":"completed","started_at":%q,"finished_at":%q,"duration_ms":1500,"external_id":"draft-build-1","error":null}]}`, t0.Add(time.Second).Format(time.RFC3339Nano), t0.Add(2500*time.Millisecond).Format(time.RFC3339Nano))
	path := filepath.Join(t.TempDir(), "execution-timings.json")
	write := func(data string) string {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(data))
		return hex.EncodeToString(digest[:])
	}
	sha := write(valid)
	runs, err := LoadSkillTimings(path, sha, expectation)
	if err != nil || len(runs) != 1 || runs[0].DurationMS != 1500 || runs[0].Attempt != 2 || runs[0].SkillSnapshotID != "snapshot-1" {
		t.Fatalf("runs=%+v err=%v", runs, err)
	}
	if _, err := LoadSkillTimings(path, strings.Repeat("0", 64), expectation); err == nil {
		t.Fatal("changed sha accepted")
	}

	cases := []struct{ name, data string }{
		{"unknown phase", strings.Replace(valid, `"draft_build","display_name":"草稿构建"`, `"asset_commit","display_name":"资产提交"`, 1)},
		{"identity mismatch", strings.Replace(valid, `"snapshot-1"`, `"snapshot-2"`, 1)},
		{"negative duration", strings.Replace(valid, `"duration_ms":1500`, `"duration_ms":-1`, 1)},
		{"duration discrepancy", strings.Replace(valid, `"duration_ms":1500`, `"duration_ms":9000`, 1)},
		{"outside bounds", strings.Replace(valid, t0.Add(time.Second).Format(time.RFC3339Nano), t0.Add(-time.Second).Format(time.RFC3339Nano), 1)},
		{"duplicate id", strings.Replace(valid, `]}`, `,{"phase_key":"input_validation","display_name":"输入校验","source":"skill","state":"completed","started_at":"2026-08-09T10:00:03Z","finished_at":"2026-08-09T10:00:04Z","duration_ms":1000,"external_id":"draft-build-1","error":null}]}`, 1)},
		{"host authority", strings.Replace(valid, `"source":"skill"`, `"source":"host"`, 1)},
		{"multiple json values", valid + ` {"schema_version":"1.0"}`},
		{"trailing non-json data", valid + ` trailing`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := LoadSkillTimings(path, write(test.data), expectation); err == nil {
				t.Fatal("invalid timing artifact accepted")
			}
		})
	}
}

func TestLoadSkillTimingsRejectsMalformedJSONAndTimestamp(t *testing.T) {
	t0 := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	expectation := ImportExpectation{TaskID: "task-1", SkillSnapshotID: "snapshot-1", Attempt: 1, NotBefore: t0, NotAfter: t0.Add(time.Minute)}
	cases := []string{
		`{"schema_version":"1.0"`,
		`{"schema_version":"1.0","task_id":"task-1","skill_snapshot_id":"snapshot-1","attempt":1,"phases":[{"phase_key":"draft_build","display_name":"草稿构建","source":"skill","state":"completed","started_at":"not-a-time","finished_at":"2026-08-09T10:00:02Z","duration_ms":1000,"external_id":"x","error":null}]}`,
		`{"schema_version":"1.0","task_id":"task-1","skill_snapshot_id":"snapshot-1","attempt":1,"phases":[{"phase_key":"draft_build","display_name":"草稿构建","source":"skill","state":"completed","started_at":"2026-08-09T10:00:01Z","finished_at":"bad-time","duration_ms":1000,"external_id":"x","error":null}]}`,
	}
	path := filepath.Join(t.TempDir(), "execution-timings.json")
	for i, data := range cases {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256([]byte(data))
			if _, err := LoadSkillTimings(path, hex.EncodeToString(digest[:]), expectation); err == nil {
				t.Fatal("malformed timing artifact accepted")
			}
		})
	}
}
