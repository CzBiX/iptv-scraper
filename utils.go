package main

import (
	"fmt"
	"net/url"
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

func findFormAction(html string) (string, error) {
	return extract(`<form action="([^"]+)"`, html)
}

func parseHiddenInputs(html string) url.Values {
	values := url.Values{}
	re := regexp.MustCompile(`<input type="hidden" name="(.+?)" value="(.*?)">`)
	matches := re.FindAllStringSubmatch(html, -1)
	for _, m := range matches {
		values.Set(m[1], m[2])
	}
	return values
}
