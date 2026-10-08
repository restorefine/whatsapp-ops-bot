// Package config parses and validates configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration.
type Config struct {
	HTTPAddr    string
	LogLevel    slog.Level
	Location    *time.Location // TZ: reports, deadlines and reminders use this
	TZLabel     string         // TZ_LABEL, e.g. "UK"
	SecondTZ    *time.Location // SECOND_TZ: shown alongside, e.g. Nepal; nil when off
	SecondLabel string         // SECOND_TZ_LABEL, e.g. "Nepal"

	WAGraphVersion  string
	WAPhoneNumberID string
	WAToken         string
	WAAppSecret     string
	WAVerifyToken   string
	WAAPIBaseURL    string
	WADryRun        bool
	Admins          []Person // ADMIN_WA_NUMBERS plus legacy OWNER_WA_NUMBER: founders and the project manager
	Team            []Person // TEAM_WA_NUMBERS: team members, who can only see and complete their own tasks

	ClickUpToken   string
	ClickUpTeamID  string
	ClickUpBaseURL string
	UploadsFolder  string // tasks in this folder are the posting calendar; empty disables

	MetricoolToken   string            // METRICOOL_API_TOKEN; empty turns Metricool off
	MetricoolUserID  string            // METRICOOL_USER_ID
	MetricoolBaseURL string            // METRICOOL_BASE_URL
	MetricoolBrands  map[string]string // METRICOOL_BRANDS: ClickUp list name → Metricool brand, for names that differ
	UploadsManual    []string          // UPLOADS_MANUAL: ClickUp lists posted by hand
	PostingTimes     [][2]int          // POSTING_TIMES: hour, minute of the morning and midday summaries; empty when off
	PostingDeadlines map[string][2]int // POSTING_DEADLINES: ClickUp list → posting deadline; "*" for every other client

	ReminderOn       bool // REMINDER_TIME is not "off"
	ReminderHour     int
	ReminderMinute   int
	ReminderTemplate string // used when an owner's 24 hour window is closed; empty skips them
	ReminderLang     string
}

// Person is someone allowed to use the bot. Name is matched against ClickUp
// member names the same way /<name> is; it may be empty for an admin who has
// no tasks in ClickUp.
type Person struct {
	Name   string // lower case, e.g. "sunil"
	Number string // digits only, with country code
}

// AdminNumbers returns the admins' phone numbers.
func (c *Config) AdminNumbers() []string {
	out := make([]string, len(c.Admins))
	for i, p := range c.Admins {
		out[i] = p.Number
	}
	return out
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

	c.MetricoolToken = get("METRICOOL_API_TOKEN", "")
	c.MetricoolUserID = get("METRICOOL_USER_ID", "")
	c.MetricoolBaseURL = strings.TrimRight(get("METRICOOL_BASE_URL", "https://app.metricool.com/api"), "/")
	if (c.MetricoolToken == "") != (c.MetricoolUserID == "") {
		problems = append(problems, "set both METRICOOL_API_TOKEN and METRICOOL_USER_ID, or neither")
	}
	c.MetricoolBrands = map[string]string{}
	for _, entry := range splitList(get("METRICOOL_BRANDS", "")) {
		list, brand, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(list) == "" || strings.TrimSpace(brand) == "" {
			problems = append(problems, fmt.Sprintf("METRICOOL_BRANDS entry %q must be ClickUp list=Metricool brand, e.g. ChocSpot=ChocStop", entry))
			continue
		}
		c.MetricoolBrands[strings.TrimSpace(list)] = strings.TrimSpace(brand)
	}
	c.UploadsManual = splitList(get("UPLOADS_MANUAL", ""))
	c.PostingDeadlines = map[string][2]int{}
	if pd := get("POSTING_DEADLINES", "*=17:00"); !strings.EqualFold(pd, "off") {
		for _, entry := range splitList(pd) {
			list, at, _ := strings.Cut(entry, "=")
			t, err := time.Parse("15:04", strings.TrimSpace(at))
			if strings.TrimSpace(list) == "" || err != nil {
				problems = append(problems, fmt.Sprintf("POSTING_DEADLINES entry %q must be client=HH:MM, e.g. failte=07:00 or *=17:00", entry))
				continue
			}
			c.PostingDeadlines[strings.TrimSpace(list)] = [2]int{t.Hour(), t.Minute()}
		}
	}
	if pt := get("POSTING_TIMES", "08:00,12:00"); !strings.EqualFold(pt, "off") {
		for _, s := range splitList(pt) {
			t, err := time.Parse("15:04", s)
			if err != nil {
				problems = append(problems, fmt.Sprintf("POSTING_TIMES %q must be HH:MM times, comma-separated, or off", pt))
				break
			}
			c.PostingTimes = append(c.PostingTimes, [2]int{t.Hour(), t.Minute()})
		}
		sort.Slice(c.PostingTimes, func(i, j int) bool {
			return c.PostingTimes[i][0]*60+c.PostingTimes[i][1] < c.PostingTimes[j][0]*60+c.PostingTimes[j][1]
		})
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
	c.Admins, problems = parsePeople("ADMIN_WA_NUMBERS", get("ADMIN_WA_NUMBERS", ""), true, problems)
	legacy, problems := parsePeople("OWNER_WA_NUMBER", get("OWNER_WA_NUMBER", ""), true, problems)
	c.Admins = append(c.Admins, legacy...)
	if len(c.Admins) == 0 {
		problems = append(problems, "missing required variable ADMIN_WA_NUMBERS (founders and project manager, e.g. suranjana=9779812345678)")
	}
	c.Team, problems = parsePeople("TEAM_WA_NUMBERS", get("TEAM_WA_NUMBERS", ""), false, problems)
	seen := map[string]bool{}
	for _, p := range append(append([]Person(nil), c.Admins...), c.Team...) {
		if seen[p.Number] {
			problems = append(problems, fmt.Sprintf("number ending %s is listed more than once across ADMIN_WA_NUMBERS, OWNER_WA_NUMBER and TEAM_WA_NUMBERS; each person needs exactly one role", tail(p.Number)))
		}
		seen[p.Number] = true
	}
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
	c.TZLabel = get("TZ_LABEL", "UK")
	if second := get("SECOND_TZ", "Asia/Kathmandu"); !strings.EqualFold(second, "off") {
		if c.SecondTZ, err = time.LoadLocation(second); err != nil {
			problems = append(problems, fmt.Sprintf("SECOND_TZ %q is not a valid IANA time zone (or off)", second))
		}
		c.SecondLabel = get("SECOND_TZ_LABEL", "Nepal")
	}

	if !versionPattern.MatchString(c.WAGraphVersion) {
		problems = append(problems, fmt.Sprintf("WA_GRAPH_VERSION %q must look like v25.0", c.WAGraphVersion))
	}

	if len(problems) > 0 {
		return nil, errors.New("invalid configuration:\n  - " + strings.Join(problems, "\n  - "))
	}
	return c, nil
}

// parsePeople reads "sunil=447700900123,himal=9779812345678". When bare is
// true a number without a name is allowed too, e.g. "447700900123".
func parsePeople(key, v string, bare bool, problems []string) ([]Person, []string) {
	var people []Person
	for _, entry := range strings.Split(v, ",") {
		if entry = strings.TrimSpace(entry); entry == "" {
			continue
		}
		name, number, ok := strings.Cut(entry, "=")
		if !ok && bare {
			name, number = "", entry
		}
		name = strings.ToLower(strings.Join(strings.Fields(name), " "))
		number = strings.TrimPrefix(strings.TrimSpace(number), "+")
		if (name == "" && !bare) || !ownerPattern.MatchString(number) {
			return nil, append(problems, fmt.Sprintf("%s entry %q must be name=number, digits only with country code, e.g. sunil=447700900123", key, entry))
		}
		people = append(people, Person{Name: name, Number: number})
	}
	return people, problems
}

// splitList splits a comma-separated value, dropping blanks.
func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// tail keeps phone numbers out of error messages apart from the last digits.
func tail(n string) string {
	if len(n) <= 3 {
		return n
	}
	return n[len(n)-3:]
}
