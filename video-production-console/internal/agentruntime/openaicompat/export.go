package openaicompat

// ExportWriterSystem returns the built-in system prompt for a style (no skill excerpt).
// Used by the remix-lab catalog so operators can inspect/edit before adopting.
func ExportWriterSystem(style string) string {
	if style == PromptStyleWash {
		return buildWashPrompt()
	}
	normalized, err := NormalizePromptStyle(style)
	if err != nil {
		normalized = PromptStyleRewrite
	}
	return buildWriterPrompt(normalized)
}
