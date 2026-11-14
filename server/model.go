package server

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const stamp = "2006-01-02T15:04:05"
const dateOnly = "2006-01-02"
const zeroDate = "0000-00-00 00:00:00"
const maxSQLTime = 838*3600 + 59*60 + 59

var networkZone = time.FixedZone("Yekaterinburg", 5*3600)
var durationPattern = regexp.MustCompile(`^(\d{1,10})([:.])([0-5]\d)([:.])([0-5]\d)$`)
var stationPattern = regexp.MustCompile(`^[0-9]{1,5}$`)

type problem struct {
	status  int
	message string
}

func (p *problem) Error() string              { return p.message }
func reject(status int, message string) error { return &problem{status, message} }

type eventText struct {
	Ticket    string `json:"ticket"`
	Direction string `json:"direction"`
	Center    string `json:"center"`
	Category  string `json:"category"`
	Network   string `json:"network"`
	Element   string `json:"element"`
	Stations  string `json:"stations"`
	Equipment string `json:"equipment"`
	Type      string `json:"type"`
	Summary   string `json:"summary"`
	Start     string `json:"start"`
	End       string `json:"end"`
	Notified  string `json:"notified"`
	Details   string `json:"details"`
}

type event struct {
	eventText
	ID           int64  `json:"id"`
	Revision     int64  `json:"revision"`
	Battery      *int64 `json:"battery"`
	Generator    *int64 `json:"generator"`
	Outage       *int64 `json:"outage"`
	Duration     *int64 `json:"duration"`
	Total        *int64 `json:"total"`
	EngineerID   int64  `json:"engineerId"`
	EngineerName string `json:"engineerName"`
	RepeatCount  int    `json:"repeatCount"`
}

type draft struct {
	eventText
	Revision   int64  `json:"revision"`
	Ongoing    bool   `json:"ongoing"`
	Battery    string `json:"battery"`
	Generator  string `json:"generator"`
	Outage     string `json:"outage"`
	EngineerID *int64 `json:"engineerId"`
}

var centers = []string{"НБР", "АСБ", "ИРБ", "КРТ", "НТГ", "НТР", "НУРЛ", "НВРТ", "НУРГ", "ОКТ", "СЛХ", "СОВ", "СРГ", "ХМН", "ЕКБ", "КМУ", "КРГ", "НСР"}

func normalizeStamp(value string) string {
	if len(value) == 16 {
		value += ":00"
	}
	t, err := time.ParseInLocation(stamp, value, networkZone)
	if err != nil || t.Year() < 1000 || t.Format(stamp) != value {
		return ""
	}
	return value
}

func durationString(seconds *int64) string {
	if seconds == nil {
		return ""
	}
	return fmt.Sprintf("%02d:%02d:%02d", *seconds/3600, *seconds/60%60, *seconds%60)
}

func durationValue(text string) (*int64, bool) {
	if strings.TrimSpace(text) == "" {
		return nil, true
	}
	parts := durationPattern.FindStringSubmatch(strings.TrimSpace(text))
	if parts == nil || parts[2] != parts[4] {
		return nil, false
	}
	hours, _ := strconv.ParseInt(parts[1], 10, 64)
	minutes, _ := strconv.ParseInt(parts[3], 10, 64)
	seconds, _ := strconv.ParseInt(parts[5], 10, 64)
	result := hours*3600 + minutes*60 + seconds
	return &result, true
}

func (e *event) compute(now time.Time) {
	e.Duration, e.Total = nil, nil
	start, err := time.ParseInLocation(stamp, e.Start, networkZone)
	if err == nil {
		finish := now
		if e.End != "" {
			finish, _ = time.ParseInLocation(stamp, e.End, networkZone)
		}
		if !finish.Before(start) {
			seconds := finish.Unix() - start.Unix()
			e.Duration = &seconds
		}
	}
	count, err := strconv.ParseInt(e.Stations, 10, 64)
	if e.Stations == "С" {
		count, err = 1, nil
	}
	if err == nil && count >= 0 && e.Outage != nil {
		total := count * *e.Outage
		e.Total = &total
	}
}

func validate(d draft, previous *event, now time.Time) (event, error) {
	e := event{eventText: d.eventText}
	var old event
	if previous != nil {
		old = *previous
	}
	fields := []struct {
		name     string
		value    *string
		previous string
		choices  []string
	}{
		{"PW / NTT", &e.Ticket, old.Ticket, nil},
		{"Направление", &e.Direction, old.Direction, []string{"ХМЯН", "СВН", "ЮЗН", "Вся сеть"}},
		{"ТЦ", &e.Center, old.Center, centers},
		{"Категория", &e.Category, old.Category, []string{"1", "2", "3"}},
		{"Тип сети", &e.Network, old.Network, []string{"GSM", "LTE", "LTE/GSM"}},
		{"Тип аварии", &e.Type, old.Type, []string{"РРС", "ССС", "ЭЛ", "RBS", "МСПД", "ТМ", "ОПС", "ПР", "АВР", "Аренда"}},
		{"Сетевой элемент", &e.Element, old.Element, nil}, {"Количество БС", &e.Stations, old.Stations, nil},
		{"Тип оборудования", &e.Equipment, old.Equipment, nil}, {"Описание события", &e.Summary, old.Summary, nil},
		{"Кому сообщили", &e.Notified, old.Notified, nil}, {"Подробное описание", &e.Details, old.Details, nil},
	}
	for _, f := range fields {
		*f.value = strings.TrimSpace(*f.value)
		if utf8.RuneCountInString(*f.value) > 450 {
			return e, reject(400, "Сократите поле «"+f.name+"» до 450 символов.")
		}
		if f.choices != nil && !slices.Contains(f.choices, *f.value) && (previous == nil || *f.value != f.previous) {
			return e, reject(400, "Выберите значение в поле «"+f.name+"».")
		}
	}
	if e.Element == "" && (previous == nil || old.Element != "") {
		return e, reject(400, "Заполните поле «Сетевой элемент».")
	}
	if e.Summary == "" && (previous == nil || old.Summary != "") {
		return e, reject(400, "Заполните поле «Описание события».")
	}
	e.Stations = strings.ToUpper(e.Stations)
	if e.Stations == "C" {
		e.Stations = "С"
	}
	count, _ := strconv.Atoi(e.Stations)
	if e.Stations != "С" && !(stationPattern.MatchString(e.Stations) && count <= 10000) && (previous == nil || e.Stations != old.Stations) {
		return e, reject(400, "Укажите количество БС от 0 до 10 000 либо «С» для сектора.")
	}
	e.Start = normalizeStamp(d.Start)
	e.End = normalizeStamp(d.End)
	if d.Ongoing {
		e.End = ""
	}
	if e.Start == "" || (e.End == "" && !d.Ongoing) {
		return e, reject(400, "Укажите начало и окончание либо отметьте «НВ».")
	}
	if (e.End != "" && e.End < e.Start) || (d.Ongoing && e.Start > now.In(networkZone).Format(stamp)) {
		return e, reject(400, "Начало аварии должно предшествовать окончанию.")
	}
	for _, item := range []struct {
		label, value string
		target       **int64
		old          *int64
	}{
		{"АКБ", d.Battery, &e.Battery, old.Battery}, {"ГУ", d.Generator, &e.Generator, old.Generator},
		{"Время перерыва связи", d.Outage, &e.Outage, old.Outage},
	} {
		seconds, ok := durationValue(item.value)
		if !ok || (seconds != nil && *seconds > maxSQLTime && (item.old == nil || *seconds != *item.old)) {
			return e, reject(400, "Поле «"+item.label+"» должно содержать длительность до 838:59:59 в формате чч:мм:сс.")
		}
		*item.target = seconds
	}
	e.compute(now)
	return e, nil
}
