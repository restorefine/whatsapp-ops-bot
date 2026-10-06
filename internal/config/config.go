// Package config parses and validates configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration.
type Config struct {
	HTTPAddr string
	LogLevel slog.Level
	Location *time.Location

	WAGraphVersion  string
	WAPhoneNumberID string
	WAToken         string
	WAAppSecret     string
	WAVerifyToken   string
	WAAPIBaseURL    string
	WADryRun        bool
	OwnerNumbers    []string      // OWNER_WA_NUMBER, comma-separated
	Team            []TeamContact // TEAM_WA_NUMBERS: who /remind can message

	ClickUpToken   string
	ClickUpTeamID  string
	ClickUpBaseURL string
	UploadsFolder  string // tasks in this folder are the posting calendar; empty disables

	ReminderOn       bool // REMINDER_TIME is not "off"
	ReminderHour     int
	ReminderMinute   int
	ReminderTemplate string // used when an owner's 24 hour window is closed; empty skips them
	ReminderLang     string
}

// TeamContact is a team member's WhatsApp number. Name is matched against
// ClickUp member names the same way /<name> is.
type TeamContact struct {
	Name   string // lower case, e.g. "sunil"
	Number string // digits only, with country code
}

var (
	ownerPattern   = regexp.MustCompile(`^[0-9]{6,15}$`)
	versionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+$`)
)

// Load reads configuration using getenv (usually os.Getenv) and validates it.
// The returned error lists every problem at once.
func Load(getenv func(string) string) (*Config, error) {
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}

	var problems []string
	require := func(key string) string {
		v := get(key, "")
		if v == "" {
			problems = append(problems, "missing required variable "+key)
		}
		return v
	}

	c := &Config{
		HTTPAddr:       get("HTTP_ADDR", ":8080"),
		WAGraphVersion: get("WA_GRAPH_VERSION", "v25.0"),
		WAAPIBaseURL:   strings.TrimRight(get("WA_API_BASE_URL", "https://graph.facebook.com"), "/"),
		ClickUpBaseURL: strings.TrimRight(get("CLICKUP_BASE_URL", "https://api.clickup.com/api/v2"), "/"),
		UploadsFolder:  get("UPLOADS_FOLDER", "Uploads"),
	}
	if strings.EqualFold(c.UploadsFolder, "none") {
		c.UploadsFolder = ""
	}

	c.ReminderTemplate = get("REMINDER_TEMPLATE", "")
	c.ReminderLang = get("REMINDER_TEMPLATE_LANG", "en")
	if rt := get("REMINDER_TIME", "off"); !strings.EqualFold(rt, "off") {
		t, err := time.Parse("15:04", rt)
		if err != nil {
			problems = append(problems, fmt.Sprintf("REMINDER_TIME %q must be HH:MM (24 hour) or off", rt))
		}
		c.ReminderOn, c.ReminderHour, c.ReminderMinute = err == nil, t.Hour(), t.Minute()
	}

	dry, err := strconv.ParseBool(get("WA_DRY_RUN", "false"))
	if err != nil {
		problems = append(problems, "WA_DRY_RUN must be true or false")
	}
	c.WADryRun = dry

	// Outbound credentials are not needed in dry-run (local development) mode.
	if c.WADryRun {
		c.WAPhoneNumberID = get("WA_PHONE_NUMBER_ID", "")
		c.WAToken = get("WA_TOKEN", "")
	} else {
		c.WAPhoneNumberID = require("WA_PHONE_NUMBER_ID")
		c.WAToken = require("WA_TOKEN")
	}
	c.WAAppSecret = require("WA_APP_SECRET")
	c.WAVerifyToken = require("WA_VERIFY_TOKEN")
	for _, n := range strings.Split(require("OWNER_WA_NUMBER"), ",") {
		if n = strings.TrimPrefix(strings.TrimSpace(n), "+"); n != "" {
			c.OwnerNumbers = append(c.OwnerNumbers, n)
		}
	}
	c.Team, problems = parseTeam(get("TEAM_WA_NUMBERS", ""), problems)
	c.ClickUpToken = require("CLICKUP_TOKEN")
	c.ClickUpTeamID = require("CLICKUP_TEAM_ID")

	if err := c.LogLevel.UnmarshalText([]byte(get("LOG_LEVEL", "info"))); err != nil {
		problems = append(problems, "LOG_LEVEL must be one of debug, info, warn, error")
	}

	tz := get("TZ", "Europe/London")
	loc, err := time.LoadLocation(tz)
	if err != nil {
		problems = append(problems, fmt.Sprintf("TZ %q is not a valid IANA time zone", tz))
		loc = time.UTC
	}
	c.Location = loc

	for _, n := range c.OwnerNumbers {
		if !ownerPattern.MatchString(n) {
			problems = append(problems, "OWNER_WA_NUMBER must be digits only with country code, comma-separated for several, e.g. 9779812345678,447700900123")
			break
		}
	}
	if !versionPattern.MatchString(c.WAGraphVersion) {
		problems = append(problems, fmt.Sprintf("WA_GRAPH_VERSION %q must look like v25.0", c.WAGraphVersion))
	}

	if len(problems) > 0 {
		return nil, errors.New("invalid configuration:\n  - " + strings.Join(problems, "\n  - "))
	}
	return c, nil
}

// parseTeam reads "sunil=447700900123,himal=9779812345678".
func parseTeam(v string, problems []string) ([]TeamContact, []string) {
	var team []TeamContact
	for _, entry := range strings.Split(v, ",") {
		if entry = strings.TrimSpace(entry); entry == "" {
			continue
		}
		name, number, _ := strings.Cut(entry, "=")
		name = strings.ToLower(strings.Join(strings.Fields(name), " "))
		number = strings.TrimPrefix(strings.TrimSpace(number), "+")
		if name == "" || !ownerPattern.MatchString(number) {
			return nil, append(problems, fmt.Sprintf("TEAM_WA_NUMBERS entry %q must be name=number, digits only with country code, e.g. sunil=447700900123", entry))
		}
		team = append(team, TeamContact{Name: name, Number: number})
	}
	return team, problems
}
