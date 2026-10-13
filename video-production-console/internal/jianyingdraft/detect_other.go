//go:build !windows

package jianyingdraft

// Detect 在非 Windows 系统上返回空候选列表。
func Detect() (Detection, error) {
	return Detection{Candidates: []Candidate{}}, nil
}
