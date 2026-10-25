package service

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"sync"

	goahocorasick "github.com/anknown/ahocorasick"
)

func InitAc(dict []string) *goahocorasick.Machine {
	m := new(goahocorasick.Machine)
	runes := readRunes(dict)
	if err := m.Build(runes); err != nil {
		fmt.Println(err)
		return nil
	}
	return m
}

var acCache sync.Map

func acKey(dict []string) string {
	if len(dict) == 0 {
		return ""
	}
	normalized := make([]string, 0, len(dict))
	for _, w := range dict {
		w = strings.ToLower(strings.TrimSpace(w))
		if w != "" {
			normalized = append(normalized, w)
		}
	}
	if len(normalized) == 0 {
		return ""
	}
	sort.Strings(normalized)
	hasher := fnv.New64a()
	for _, w := range normalized {
		hasher.Write([]byte{0})
		hasher.Write([]byte(w))
	}
	return fmt.Sprintf("%x", hasher.Sum64())
}

func getOrBuildAC(dict []string) *goahocorasick.Machine {
	key := acKey(dict)
	if key == "" {
		return nil
	}
	if v, ok := acCache.Load(key); ok {
		if m, ok2 := v.(*goahocorasick.Machine); ok2 {
			return m
		}
	}
	m := InitAc(dict)
	if m == nil {
		return nil
	}
	if actual, loaded := acCache.LoadOrStore(key, m); loaded {
		if cached, ok := actual.(*goahocorasick.Machine); ok {
			return cached
		}
	}
	return m
}

func readRunes(dict []string) [][]rune {
	var runes [][]rune

	for _, word := range dict {
		word = strings.ToLower(word)
		l := bytes.TrimSpace([]byte(word))
		runes = append(runes, bytes.Runes(l))
	}

	return runes
}

func AcSearch(findText string, dict []string, stopImmediately bool) (bool, []string) {
	if len(dict) == 0 {
		return false, nil
	}
	if len(findText) == 0 {
		return false, nil
	}
	m := getOrBuildAC(dict)
	if m == nil {
		return false, nil
	}
	hits := m.MultiPatternSearch([]rune(findText), stopImmediately)
	if len(hits) > 0 {
		words := make([]string, 0)
		for _, hit := range hits {
			words = append(words, string(hit.Word))
		}
		return true, words
	}
	return false, nil
}
