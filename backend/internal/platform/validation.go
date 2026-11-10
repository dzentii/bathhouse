package platform

import (
	"net/http"
	"strconv"
	"unicode"
)

func SafeText(value string, min, max int, multiline bool) bool {
	n := len([]rune(value))
	if n < min || n > max {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) && (!multiline || r != '\n' && r != '\t') {
			return false
		}
	}
	return true
}
func Page(r *http.Request) (int, int, error) {
	limit, offset := 50, 0
	var err error
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, err = strconv.Atoi(v)
		if err != nil {
			return 0, 0, Bad("Некорректный limit")
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		offset, err = strconv.Atoi(v)
		if err != nil {
			return 0, 0, Bad("Некорректный offset")
		}
	}
	if limit < 1 || limit > 100 || offset < 0 || offset > 10000 {
		return 0, 0, Bad("limit: 1..100, offset: 0..10000")
	}
	return limit, offset, nil
}
