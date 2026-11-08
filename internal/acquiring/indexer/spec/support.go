package spec

import (
	"slices"
	"strings"

	"golang.org/x/text/encoding/htmlindex"
)

// Unsupported lists why the engine cannot search the spec; empty means it can.
func (s Spec) Unsupported() []string {
	var reasons []string
	add := func(reason string) {
		if !slices.Contains(reasons, reason) {
			reasons = append(reasons, reason)
		}
	}
	if login := s.Login; login != nil {
		switch login.Method {
		case "", "none", "cookie", "post", "form", "get":
		default:
			add("sign-in by " + login.Method)
		}
		if login.Captcha != nil && login.Captcha.Type != "" {
			add("the sign-in asks a captcha")
		}
	}
	if len(s.Search.Paths) == 0 {
		add("no search path")
	}
	if s.Search.Rows.DateHeaders != nil && string(s.Search.Rows.DateHeaders) != "null" {
		add("results grouped under date headings")
	}
	if enc := strings.ToLower(s.Encoding); enc != "" && enc != "utf-8" && enc != "utf8" {
		if _, err := htmlindex.Get(enc); err != nil {
			add("the " + s.Encoding + " character set")
		}
	}
	check := func(text string) {
		if strings.Contains(text, "{{") {
			if _, err := parseTemplate(text); err != nil {
				add("a template Solarr cannot read")
			}
		}
	}
	checkFilters := func(filters []Filter) {
		for _, filter := range filters {
			if !knownFilters[filter.Name] {
				add("the " + filter.Name + " filter")
			}
			for _, arg := range filter.Args {
				check(arg)
			}
		}
	}
	checkExtractor := func(extractor Extractor) {
		check(extractor.Selector)
		if extractor.Text != nil {
			check(*extractor.Text)
		}
		checkFilters(extractor.Filters)
		for _, one := range extractor.Case {
			check(one.Selector)
			check(one.Value)
		}
	}
	for _, field := range s.Search.Fields {
		checkExtractor(field)
	}
	checkFilters(s.Search.KeywordsFilters)
	checkFilters(s.Search.PreprocessingFilters)
	checkFilters(s.Search.Rows.Filters)
	for _, path := range s.Search.Paths {
		check(path.Path)
		for _, input := range path.Inputs {
			check(input)
		}
	}
	for _, input := range s.Search.Inputs {
		check(input)
	}
	check(s.Search.Rows.Selector)
	if s.Download != nil {
		for _, selector := range s.Download.Selectors {
			checkExtractor(selector)
		}
	}
	return reasons
}
