package main

import (
	"encoding/json"
	"encoding/xml"
	"log/slog"
	"strings"
	"time"
)

type XMLTV struct {
	XMLName           xml.Name       `xml:"tv"`
	GeneratorInfoName string         `xml:"generator-info-name,attr"`
	Programmes        []XMLProgramme `xml:"programme"`
}

type XMLProgramme struct {
	Start   string `xml:"start,attr"`
	Stop    string `xml:"stop,attr"`
	Channel string `xml:"channel,attr"`
	Title   string `xml:"title"`
}

type ChannelProgramList struct {
	List []Program `json:"result"`
}

type Program struct {
	Title     string `json:"name"`
	StartTime string `json:"time"`
	EndTime   string `json:"endtime"`
}

func fetchEPGData(channels []Channel, authClient *AuthClient) ([]byte, error) {
	// Fetch EPG data for the past 7 days
	now := time.Now()
	dates := make([]string, 7)
	for i := range 7 {
		diff := 1 - i
		dates[i] = now.AddDate(0, 0, diff).Format("20060102")
	}

	var allProgrammes []XMLProgramme

	for _, ch := range channels {
		if ch.TimeShiftURL == "" {
			continue
		}

		slog.Info("Fetching EPG", "channel", ch.Name)

		for _, date := range dates {
			slog.Debug("Fetching EPG", "channel", ch.Name, "date", date)
			data, err := authClient.getEPGData(ch.ChannelID, date)
			if err != nil {
				slog.Error("Failed to fetch EPG", "channel", ch.Name, "date", date, "err", err)
				continue
			}

			var channelProgramList ChannelProgramList
			if err := json.Unmarshal(data, &channelProgramList); err != nil {
				slog.Error("Failed to unmarshal EPG data", "channel", ch.Name, "date", date, "err", err)
				continue
			}

			for _, p := range channelProgramList.List {
				prog := XMLProgramme{
					Start:   parseTimeToXMLTV(date, p.StartTime),
					Stop:    parseTimeToXMLTV(date, p.EndTime),
					Channel: ch.ChannelID,
					Title:   p.Title,
				}
				allProgrammes = append(allProgrammes, prog)
			}
		}
	}

	return buildEPGXML(allProgrammes)
}

func buildEPGXML(programmes []XMLProgramme) ([]byte, error) {
	tv := XMLTV{
		GeneratorInfoName: "iptv-scraper",
		Programmes:        programmes,
	}

	out, err := xml.MarshalIndent(tv, "", "  ")
	if err != nil {
		return nil, err
	}

	return append([]byte(xml.Header), out...), nil
}

func parseTimeToXMLTV(date, t string) string {
	// "00:10:15" -> "20260228001015 +0800"
	t = strings.ReplaceAll(t, ":", "")
	return date + t + " +0800"
}
