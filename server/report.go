package server

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

var reportHeaders = []string{
	"PW / NTT", "Направление", "ТЦ", "Категория", "Тип сети", "Сетевой элемент / услуга",
	"Количество БС", "Тип оборудования", "Тип аварии", "Описание", "Начало", "Окончание",
	"АКБ", "ГУ", "Длительность аварии", "Время перерыва связи", "Общее время простоя БС",
	"Кому сообщили", "Подробное описание",
}

func reportRow(e event) []string {
	end := "НВ"
	if e.End != "" {
		end = strings.Replace(e.End, "T", " ", 1)
	}
	return []string{
		e.Ticket, e.Direction, e.Center, e.Category, e.Network, e.Element, e.Stations, e.Equipment,
		e.Type, e.Summary, strings.Replace(e.Start, "T", " ", 1), end, durationString(e.Battery),
		durationString(e.Generator), durationString(e.Duration), durationString(e.Outage),
		durationString(e.Total), e.Notified, e.Details,
	}
}

func attachment(w http.ResponseWriter, contentType, extension, from, to string) {
	name := "Аварии_" + from
	if from != to {
		name += "_" + to
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + "." + extension}))
}

func writeCSV(w http.ResponseWriter, events []event, from, to string) {
	attachment(w, "text/csv; charset=utf-8", "csv", from, to)
	_, _ = io.WriteString(w, "\ufeff")
	writer := csv.NewWriter(w)
	writer.Comma, writer.UseCRLF = ';', true
	_ = writer.Write(reportHeaders)
	for _, e := range events {
		row := reportRow(e)
		for i, value := range row {
			trimmed := strings.TrimSpace(value)
			if strings.HasPrefix(value, "\t") || strings.HasPrefix(value, "\r") || strings.HasPrefix(value, "\n") ||
				(trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0]))) {
				row[i] = "'" + value
			}
		}
		_ = writer.Write(row)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		log.Printf("write CSV: %v", err)
	}
}

func writeXLSX(w http.ResponseWriter, events []event, from, to string) error {
	file := excelize.NewFile()
	defer file.Close()
	if err := buildWorkbook(file, events); err != nil {
		return err
	}
	content, err := file.WriteToBuffer()
	if err != nil {
		return err
	}
	attachment(w, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "xlsx", from, to)
	w.Header().Set("Content-Length", strconv.Itoa(content.Len()))
	if _, err := content.WriteTo(w); err != nil {
		log.Printf("write XLSX: %v", err)
	}
	return nil
}

func buildWorkbook(file *excelize.File, events []event) error {
	const sheet = "Аварии"
	if err := file.SetSheetName("Sheet1", sheet); err != nil {
		return err
	}
	if err := file.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return err
	}

	header, err := file.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"58447B"}},
		Alignment: &excelize.Alignment{WrapText: true, Vertical: "center"},
	})
	if err != nil {
		return err
	}
	dateFormat, timeFormat := "dd.mm.yyyy hh:mm:ss", "[h]:mm:ss"
	styles := make([][3]int, 2)
	for i, color := range []string{"FFFFFF", "F4EFFA"} {
		for j, format := range []*string{nil, &dateFormat, &timeFormat} {
			id, err := file.NewStyle(&excelize.Style{
				Fill:         excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{color}},
				Alignment:    &excelize.Alignment{WrapText: true, Vertical: "top"},
				CustomNumFmt: format,
			})
			if err != nil {
				return err
			}
			styles[i][j] = id
		}
	}
	widths := []float64{22, 16, 10, 12, 13, 40, 14, 22, 16, 37, 23, 23, 16, 16, 16, 18, 20, 28, 58}
	for i, width := range widths {
		col, err := excelize.ColumnNumberToName(i + 1)
		if err != nil {
			return err
		}
		if err := file.SetColWidth(sheet, col, col, width); err != nil {
			return err
		}
	}
	if err := file.SetRowHeight(sheet, 1, 34); err != nil {
		return err
	}
	if err := file.SetSheetRow(sheet, "A1", &reportHeaders); err != nil {
		return err
	}
	if err := file.SetCellStyle(sheet, "A1", "S1", header); err != nil {
		return err
	}
	if err := file.AutoFilter(sheet, fmt.Sprintf("A1:S%d", len(events)+1), nil); err != nil {
		return err
	}

	for i, e := range events {
		rowNumber := i + 2
		height := 1
		style := styles[i%2]
		for j, value := range reportRow(e) {
			cell, err := excelize.CoordinatesToCellName(j+1, rowNumber)
			if err != nil {
				return err
			}
			var data any = value
			styleID := style[0]
			switch j {
			case 3, 6:
				if count, err := strconv.Atoi(value); err == nil {
					data = count
				}
			case 10, 11:
				if date, err := time.Parse("2006-01-02 15:04:05", value); err == nil && date.Year() >= 1900 {
					data, styleID = date, style[1]
				}
			case 12, 13, 14, 15, 16:
				if seconds, ok := durationValue(value); ok && seconds != nil {
					data, styleID = float64(*seconds)/86400, style[2]
				}
			}
			if err := file.SetCellValue(sheet, cell, data); err != nil {
				return err
			}
			if err := file.SetCellStyle(sheet, cell, cell, styleID); err != nil {
				return err
			}
			if _, isText := data.(string); isText {
				lines := 0
				usable := max(1, int(widths[j])-3)
				for _, line := range strings.Split(value, "\n") {
					lines += max(1, (utf8.RuneCountInString(line)+usable-1)/usable)
				}
				height = max(height, lines)
			}
		}
		if err := file.SetRowHeight(sheet, rowNumber, float64(min(390, max(22, 7+height*16)))); err != nil {
			return err
		}
	}
	return nil
}
