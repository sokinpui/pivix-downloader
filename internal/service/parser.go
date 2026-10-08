package service

import (
	"regexp"
	"strings"
)

var pixivURLRegexes = []*regexp.Regexp{
	regexp.MustCompile(`pixiv\.net/(?:en/)?artworks/(\d+)`),
	regexp.MustCompile(`pixiv\.net/member_illust\.php\?.*illust_id=(\d+)`),
	regexp.MustCompile(`^(\d+)$`),
}

func ParseArtworkID(input string) string {
	input = strings.TrimSpace(input)
	for _, re := range pixivURLRegexes {
		matches := re.FindStringSubmatch(input)
		if len(matches) >= 2 {
			return matches[1]
		}
	}
	return ""
}
