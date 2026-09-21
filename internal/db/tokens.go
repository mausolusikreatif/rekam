package db

// CountTokens estimates tokens using ~4 chars per token (GPT-4 approximation).
func CountTokens(text string) int {
	if text == "" {
		return 0
	}
	return (len([]rune(text)) + 3) / 4
}
