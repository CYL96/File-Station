package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

type fileViewColumn struct {
	Key     string `json:"key"`
	Visible bool   `json:"visible"`
}

type fileViewSort struct {
	Key       string `json:"key"`
	Ascending bool   `json:"ascending"`
}

type fileViewSettings struct {
	ShowHidden bool             `json:"showHidden"`
	Columns    []fileViewColumn `json:"columns"`
	Sort       fileViewSort     `json:"sort"`
}

// Pointer fields distinguish missing or null booleans from an explicit false.
type fileViewInput struct {
	ShowHidden *bool `json:"showHidden"`
	Columns    []struct {
		Key     string `json:"key"`
		Visible *bool  `json:"visible"`
	} `json:"columns"`
	Sort *struct {
		Key       string `json:"key"`
		Ascending *bool  `json:"ascending"`
	} `json:"sort"`
}

func defaultFileViewSettings() fileViewSettings {
	return fileViewSettings{
		Columns: []fileViewColumn{
			{"name", true}, {"size", true}, {"modified", true}, {"type", true},
			{"extension", false}, {"permissions", false}, {"owner", false}, {"group", false}, {"created", false},
		},
		Sort: fileViewSort{Key: "name", Ascending: true},
	}
}

func fileViewKeyIndex(key string) int {
	switch key {
	case "name":
		return 0
	case "size":
		return 1
	case "modified":
		return 2
	case "type":
		return 3
	case "extension":
		return 4
	case "permissions":
		return 5
	case "owner":
		return 6
	case "group":
		return 7
	case "created":
		return 8
	default:
		return -1
	}
}

func (input fileViewInput) settings() (fileViewSettings, error) {
	return input.settingsForColumns(9)
}

func (input fileViewInput) settingsForColumns(count int) (fileViewSettings, error) {
	invalid := func() (fileViewSettings, error) {
		return fileViewSettings{}, fail(http.StatusBadRequest, "文件视图设置格式错误")
	}
	sortIndex := -1
	if input.Sort != nil {
		sortIndex = fileViewKeyIndex(input.Sort.Key)
	}
	if input.ShowHidden == nil || len(input.Columns) != count || input.Sort == nil || input.Sort.Ascending == nil || sortIndex < 0 || sortIndex >= count {
		return invalid()
	}
	var seen [9]bool
	for _, column := range input.Columns {
		index := fileViewKeyIndex(column.Key)
		if index < 0 || index >= count || seen[index] || column.Visible == nil || (column.Key == "name" && !*column.Visible) {
			return invalid()
		}
		seen[index] = true
	}
	settings := fileViewSettings{
		ShowHidden: *input.ShowHidden,
		Columns:    make([]fileViewColumn, len(input.Columns)),
		Sort:       fileViewSort{Key: input.Sort.Key, Ascending: *input.Sort.Ascending},
	}
	for i, column := range input.Columns {
		settings.Columns[i] = fileViewColumn{Key: column.Key, Visible: *column.Visible}
	}
	return settings, nil
}

func (s *Server) fileSettings(w http.ResponseWriter, r *http.Request) error {
	for {
		var document string
		err := s.db.QueryRowContext(r.Context(), "SELECT value FROM settings_meta WHERE key='file_view'").Scan(&document)
		if errors.Is(err, sql.ErrNoRows) {
			jsonResponse(w, defaultFileViewSettings())
			return nil
		}
		if err != nil {
			return fail(http.StatusInternalServerError, "读取文件视图设置失败")
		}
		var input fileViewInput
		decoder := json.NewDecoder(strings.NewReader(document))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&input); err != nil {
			return fail(http.StatusInternalServerError, "已保存的文件视图设置无效")
		}
		if decoder.Decode(&struct{}{}) != io.EOF {
			return fail(http.StatusInternalServerError, "已保存的文件视图设置无效")
		}
		count := 9
		if len(input.Columns) == 4 {
			count = 4
		}
		settings, err := input.settingsForColumns(count)
		if err != nil {
			return fail(http.StatusInternalServerError, "已保存的文件视图设置无效")
		}
		if count == 4 {
			settings.Columns = append(settings.Columns, defaultFileViewSettings().Columns[4:]...)
			migrated, err := json.Marshal(settings)
			if err != nil {
				return err
			}
			// A save after the read wins; never replace it with the legacy migration.
			result, err := s.db.ExecContext(r.Context(), "UPDATE settings_meta SET value=? WHERE key='file_view' AND value=?", string(migrated), document)
			if err != nil {
				return fail(http.StatusInternalServerError, "保存文件视图设置失败")
			}
			changed, err := result.RowsAffected()
			if err != nil {
				return fail(http.StatusInternalServerError, "保存文件视图设置失败")
			}
			if changed == 0 {
				continue
			}
		}
		jsonResponse(w, settings)
		return nil
	}
}

func (s *Server) saveFileSettings(w http.ResponseWriter, r *http.Request) error {
	var input fileViewInput
	if err := decode(w, r, &input); err != nil {
		return err
	}
	settings, err := input.settings()
	if err != nil {
		return err
	}
	document, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(r.Context(), "INSERT INTO settings_meta(key,value) VALUES('file_view',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", string(document))
	if err != nil {
		return fail(http.StatusInternalServerError, "保存文件视图设置失败")
	}
	jsonResponse(w, settings)
	return nil
}
