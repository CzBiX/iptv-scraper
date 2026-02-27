package main

import (
	"bytes"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const epgURL = "https://gh-proxy.org/https://github.com/mytv-android/myEPG/raw/refs/heads/master/output/epg.gz"

var groupOrder = []string{"央视", "湖北", "卫视", "影视", "4K", "其他"}

type Channel struct {
	ChannelID    string
	Name         string
	URL          string
	TimeShiftURL string
	FCC          string
}

func getGroup(name string) string {
	nameUp := strings.ToUpper(name)
	if strings.Contains(name, "4K") {
		return "4K"
	}
	if strings.Contains(nameUp, "CCTV") || strings.Contains(name, "中央") {
		return "央视"
	}
	if strings.Contains(name, "湖北") {
		return "湖北"
	}
	if strings.Contains(name, "卫视") {
		return "卫视"
	}
	for _, x := range []string{"数字", "CHC", "影院", "剧场"} {
		if strings.Contains(nameUp, x) {
			return "影视"
		}
	}
	for _, x := range []string{"购物", "精选"} {
		if strings.Contains(nameUp, x) {
			return ""
		}
	}
	return "其他"
}

func parseChannel(line string) Channel {
	re := regexp.MustCompile(`\b(.+?)="(.*?)"`)
	matches := re.FindAllStringSubmatch(line, -1)
	attrs := make(map[string]string)
	for _, m := range matches {
		attrs[m[1]] = m[2]
	}

	var fcc string
	if attrs["FCCEnable"] == "1" {
		fcc = fmt.Sprintf("%s:%s", attrs["ChannelFCCIP"], attrs["ChannelFCCPort"])
	}

	return Channel{
		ChannelID:    attrs["UserChannelID"],
		Name:         attrs["ChannelName"],
		URL:          attrs["ChannelURL"],
		TimeShiftURL: attrs["TimeShiftURL"],
		FCC:          fcc,
	}
}

func buildM3U(channels []Channel) string {
	sort.SliceStable(channels, func(i, j int) bool {
		id1, _ := strconv.Atoi(channels[i].ChannelID)
		id2, _ := strconv.Atoi(channels[j].ChannelID)
		return id1 < id2
	})

	groups := make(map[string][]Channel)
	for _, ch := range channels {
		groupName := getGroup(ch.Name)
		if groupName == "" {
			slog.Info("Skipping (filtered group)", "channel", ch.Name)
			continue
		}
		groups[groupName] = append(groups[groupName], ch)
	}

	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("#EXTM3U x-tvg-url=\"%s\"\n\n", epgURL))
	count := 0

	for _, key := range groupOrder {
		for _, ch := range groups[key] {
			if !strings.HasPrefix(ch.URL, "igmp") {
				slog.Info("Skipping (non-igmp)", "channel", ch.Name)
				continue
			}

			finalURL := strings.Replace(ch.URL, "igmp://", "rtp://", 1)
			fccArg := ""
			if ch.FCC != "" {
				fccArg = "?fcc=" + ch.FCC
			}

			displayName := strings.TrimSuffix(ch.Name, "HD")

			var attrs []string
			attrs = append(attrs, fmt.Sprintf("tvg-id=\"%s\"", displayName))
			attrs = append(attrs, fmt.Sprintf("tvg-name=\"%s\"", displayName))
			attrs = append(attrs, fmt.Sprintf("group-title=\"%s\"", key))

			if ch.TimeShiftURL != "" {
				attrs = append(attrs, "catchup=\"default\"")
				attrs = append(attrs, fmt.Sprintf("catchup-source=\"%s&playseek={utc:YmdHMS}-{utcend:YmdHMS}\"", ch.TimeShiftURL))
			}

			buf.WriteString(fmt.Sprintf("#EXTINF:-1 %s,%s\n", strings.Join(attrs, " "), displayName))
			buf.WriteString(fmt.Sprintf("%s%s\n", finalURL, fccArg))
			count++
		}
	}

	slog.Info("Channel parsing complete", "parsed", len(channels), "used", count)
	return buf.String()
}

func getChannelList(channels []string) string {
	var parsedChannels []Channel
	for _, line := range channels {
		parsedChannels = append(parsedChannels, parseChannel(line))
	}
	return buildM3U(parsedChannels)
}
