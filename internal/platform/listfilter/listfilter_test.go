package listfilter_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/platform/listfilter"
)

var fields = map[string]listfilter.Field{
	"name":      {Kind: listfilter.Text},
	"series":    {Kind: listfilter.Number},
	"monitored": {Kind: listfilter.Bool},
	"role":      {Kind: listfilter.Enum, Choices: []string{"actor", "director"}},
}

func TestParse_AcceptsKnownFieldsAndReportsEveryProblem(t *testing.T) {
	got, err := listfilter.Parse(`[{"field":"series","op":"gt","value":"3"},{"field":"role","op":"is","value":"actor"}]`, fields)
	if err != nil || len(got) != 2 {
		t.Fatalf("Parse = %+v, %v", got, err)
	}
	_, err = listfilter.Parse(`[{"field":"nope","op":"is","value":"x"},{"field":"series","op":"contains","value":"x"},{"field":"series","op":"gt","value":"many"},{"field":"role","op":"is","value":"cook"},{"field":"monitored","op":"is","value":"maybe"}]`, fields)
	if err == nil {
		t.Fatal("want problems")
	}
	for _, want := range []string{`condition 1: "nope"`, `condition 2: "series" does not take "contains"`, `condition 3: "many" is not a number`, `condition 4: "cook" is not one of actor, director`, `condition 5: "maybe"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
	}
	if none, err := listfilter.Parse("", fields); none != nil || err != nil {
		t.Errorf("empty = %+v, %v", none, err)
	}
}

func TestCondition_SQL(t *testing.T) {
	cases := []struct {
		condition listfilter.Condition
		kind      listfilter.Kind
		sql       string
		args      []any
	}{
		{listfilter.Condition{Field: "name", Op: "contains", Value: "50%_Cent"}, listfilter.Text, `LOWER(p.name) LIKE ? ESCAPE '\'`, []any{`%50\%\_cent%`}},
		{listfilter.Condition{Field: "name", Op: "is_not", Value: "Bob"}, listfilter.Text, `LOWER(p.name) <> ?`, []any{"bob"}},
		{listfilter.Condition{Field: "series", Op: "ge", Value: "3"}, listfilter.Number, `(p.n) >= ?`, []any{3.0}},
		{listfilter.Condition{Field: "monitored", Op: "is", Value: "false"}, listfilter.Bool, `NOT (p.m)`, nil},
		{listfilter.Condition{Field: "role", Op: "is_not", Value: "actor"}, listfilter.Enum, `(p.r) <> ?`, []any{"actor"}},
	}
	for _, c := range cases {
		expression := map[listfilter.Kind]string{listfilter.Text: "p.name", listfilter.Number: "p.n", listfilter.Bool: "p.m", listfilter.Enum: "p.r"}[c.kind]
		sql, args := c.condition.SQL(c.kind, expression)
		if sql != c.sql || !reflect.DeepEqual(args, c.args) {
			t.Errorf("%+v: SQL = %q %v, want %q %v", c.condition, sql, args, c.sql, c.args)
		}
	}
}
