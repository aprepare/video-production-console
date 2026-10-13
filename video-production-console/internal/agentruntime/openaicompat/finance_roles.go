package openaicompat

// FinanceWriterRole exposes the existing finance writer role without
// duplicating or changing its prompt text.
func FinanceWriterRole() string {
	return writerRolePrompt
}

// FinanceReviewerRole exposes the existing finance reviewer role without
// duplicating or changing its prompt text.
func FinanceReviewerRole() string {
	return reviewerRolePrompt
}
