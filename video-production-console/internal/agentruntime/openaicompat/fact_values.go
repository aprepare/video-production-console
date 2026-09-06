package openaicompat

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"math/big"
	"regexp"
	"strings"
)

var numericPartsRe = regexp.MustCompile(`^(\d+(?:\.\d+)?)(.*)$`)
var sourceURLRe = regexp.MustCompile(`https?://[^\s"<>]+`)
var sourceDateRe = regexp.MustCompile(`(?:19|20)\d{2}[-/年]\d{1,2}(?:[-/月]\d{1,2})?`)
var arithmeticEvidenceRe = regexp.MustCompile(`([\d.]+(?:\s*[*×/÷+−-]\s*[\d.]+[%％]?)+)\s*=\s*([\d.]+)`)
var percentLiteralRe = regexp.MustCompile(`(\d+(?:\.\d+)?)[%％]`)

// Compare numeric values with units, never by substring or by rounding.
func numericValueKey(value string) string {
	m := numericPartsRe.FindStringSubmatch(value)
	if len(m) != 3 {
		return value
	}
	n, ok := new(big.Rat).SetString(m[1])
	if !ok {
		return value
	}
	unit := m[2]
	if unit == "%" || unit == "％" {
		return "percent:" + n.RatString()
	}
	multiplier := int64(1)
	switch unit {
	case "万亿元", "万亿":
		multiplier = 1000000000000
	case "亿元", "亿":
		multiplier = 100000000
	case "万元", "万":
		multiplier = 10000
	case "元", "块":
	default:
		return value
	}
	n.Mul(n, new(big.Rat).SetInt64(multiplier))
	return "amount:" + n.RatString()
}

func numericValues(text string) map[string]bool {
	out := map[string]bool{}
	for value := range lockNumberTokens(text, numericFactRe) {
		out[numericValueKey(value)] = true
	}
	return out
}

func hasNumericValue(text, value string) bool { return numericValues(text)[numericValueKey(value)] }

// An explicit URL/date is a provenance requirement, not proof that a source
// supports a claim. The fact checker and semantic reviewer still verify scope.
func supportedSource(raw json.RawMessage) bool {
	s := string(raw)
	if sourceURLRe.MatchString(s) && sourceDateRe.MatchString(s) {
		return true
	}
	for _, m := range arithmeticEvidenceRe.FindAllStringSubmatch(s, -1) {
		expression := strings.NewReplacer("×", "*", "÷", "/", "−", "-").Replace(m[1])
		expression = percentLiteralRe.ReplaceAllString(expression, "($1/100)")
		expr, err := parser.ParseExpr(expression)
		if err != nil {
			continue
		}
		got, ok := arithmeticValue(expr)
		want, valid := new(big.Rat).SetString(m[2])
		if ok && valid && got.Cmp(want) == 0 {
			return true
		}
	}
	return false
}

// Only arithmetic literals and the four operations are evaluated. No names,
// calls, shell evaluation, floating point rounding, or execution is involved.
func arithmeticValue(expr ast.Expr) (*big.Rat, bool) {
	switch v := expr.(type) {
	case *ast.BasicLit:
		return new(big.Rat).SetString(v.Value)
	case *ast.ParenExpr:
		return arithmeticValue(v.X)
	case *ast.BinaryExpr:
		a, ok := arithmeticValue(v.X)
		if !ok {
			return nil, false
		}
		b, ok := arithmeticValue(v.Y)
		if !ok {
			return nil, false
		}
		switch v.Op {
		case token.ADD:
			return a.Add(a, b), true
		case token.SUB:
			return a.Sub(a, b), true
		case token.MUL:
			return a.Mul(a, b), true
		case token.QUO:
			if b.Sign() != 0 {
				return a.Quo(a, b), true
			}
		}
	}
	return nil, false
}
