package security

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

const Mask = "***"

var (
	authorizationPattern = regexp.MustCompile(`(?im)(authorization[ \t]*:[ \t]*)[^\r\n]*`)
	environmentPattern   = regexp.MustCompile(`(?i)\b([a-z0-9_]*(?:api_key|token|password|secret))[ \t]*=[ \t]*(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;}]+)`)
	jsonPattern          = regexp.MustCompile(`(?i)("(?:\\.|[^"\\])*(?:_api_key|token|password|secret)(?:\\.|[^"\\])*"[ \t\r\n]*:[ \t\r\n]*)"(?:\\.|[^"\\])*"`)
)

type Redactor struct {
	mu      sync.RWMutex
	secrets []string
}

func NewRedactor() *Redactor { return &Redactor{} }

func (r *Redactor) Register(secret string) {
	if secret == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.secrets {
		if existing == secret {
			return
		}
	}
	r.secrets = append(r.secrets, secret)
	sort.Slice(r.secrets, func(i, j int) bool { return len(r.secrets[i]) > len(r.secrets[j]) })
}

func (r *Redactor) Redact(value string) string {
	r.mu.RLock()
	secrets := append([]string(nil), r.secrets...)
	r.mu.RUnlock()
	for _, secret := range secrets {
		value = strings.ReplaceAll(value, secret, Mask)
	}
	value = authorizationPattern.ReplaceAllString(value, `${1}`+Mask)
	value = environmentPattern.ReplaceAllString(value, `${1}=`+Mask)
	return jsonPattern.ReplaceAllString(value, `${1}"`+Mask+`"`)
}
