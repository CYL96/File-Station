package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

type generalSettings struct {
	DateFormat       string `json:"dateFormat"`
	PermissionFormat string `json:"permissionFormat"`
}

type generalSettingsInput struct {
	DateFormat       *string         `json:"dateFormat"`
	PermissionFormat json.RawMessage `json:"permissionFormat"`
}

func defaultGeneralSettings() generalSettings {
	return generalSettings{DateFormat: "locale", PermissionFormat: "octal"}
}

func (input generalSettingsInput) settings() (generalSettings, error) {
	if input.DateFormat == nil {
		return generalSettings{}, fail(http.StatusBadRequest, "通用设置格式错误")
	}
	switch *input.DateFormat {
	case "locale", "yyyy-MM-dd", "yyyy/MM/dd", "dd/MM/yyyy", "MM/dd/yyyy":
	default:
		return generalSettings{}, fail(http.StatusBadRequest, "通用设置格式错误")
	}
	settings := defaultGeneralSettings()
	settings.DateFormat = *input.DateFormat
	if input.PermissionFormat != nil {
		var format string
		if err := json.Unmarshal(input.PermissionFormat, &format); err != nil {
			return generalSettings{}, fail(http.StatusBadRequest, "通用设置格式错误")
		}
		switch format {
		case "octal", "symbolic":
			settings.PermissionFormat = format
		default:
			return generalSettings{}, fail(http.StatusBadRequest, "通用设置格式错误")
		}
	}
	return settings, nil
}

func (s *Server) generalSettings(w http.ResponseWriter, r *http.Request) error {
	var document string
	err := s.db.QueryRowContext(r.Context(), "SELECT value FROM settings_meta WHERE key='general'").Scan(&document)
	if errors.Is(err, sql.ErrNoRows) {
		jsonResponse(w, defaultGeneralSettings())
		return nil
	}
	if err != nil {
		return fail(http.StatusInternalServerError, "读取通用设置失败")
	}
	var input generalSettingsInput
	decoder := json.NewDecoder(strings.NewReader(document))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&input); err != nil {
		return fail(http.StatusInternalServerError, "已保存的通用设置无效")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return fail(http.StatusInternalServerError, "已保存的通用设置无效")
	}
	settings, err := input.settings()
	if err != nil {
		return fail(http.StatusInternalServerError, "已保存的通用设置无效")
	}
	jsonResponse(w, settings)
	return nil
}

func (s *Server) saveGeneralSettings(w http.ResponseWriter, r *http.Request) error {
	var input generalSettingsInput
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
	_, err = s.db.ExecContext(r.Context(), "INSERT INTO settings_meta(key,value) VALUES('general',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", string(document))
	if err != nil {
		return fail(http.StatusInternalServerError, "保存通用设置失败")
	}
	jsonResponse(w, settings)
	return nil
}
