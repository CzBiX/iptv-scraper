package main

import (
	"fmt"
	"regexp"
)

func extract(pattern string, text string) (string, error) {
	re := regexp.MustCompile(pattern)
	matches := re.FindStringSubmatch(text)
	if len(matches) < 2 {
		return "", fmt.Errorf("failed to extract %s", pattern)
	}
	return matches[1], nil
}

func parseHiddenInputs(html string) map[string]string {
	pairs := make(map[string]string)
	re := regexp.MustCompile(`<input type="hidden" name="(.+?)" value="(.+?)">`)
	matches := re.FindAllStringSubmatch(html, -1)
	for _, m := range matches {
		pairs[m[1]] = m[2]
	}
	return pairs
}
