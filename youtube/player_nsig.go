package youtube

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
)

// extractNSigScript finds the combined URL-mutating nsig function in a
// YouTube player script and returns JavaScript source defining it plus the
// safe transitive dependency declarations it needs.
//
// Candidate shape: a function expression with at least three parameters. Its
// body reassigns the first parameter through a URL-like constructor and calls
// an object's .set("alr", "yes") method.
//
// Only side-effect-free dependency initializers (function expressions,
// arrow functions, and plain literals) are carried over; anything else
// produces an error instead of being evaluated.
func extractNSigScript(source []byte) (string, error) {
	prog, err := parser.ParseFile(nil, "player.js", string(source), 0)
	if err != nil {
		return "", fmt.Errorf("nsig: parse player script: %w", err)
	}
	candidate := findNSigCandidate(prog)
	if candidate.function == nil {
		return "", errors.New("nsig: no candidate function found (expected a URL transform with >=3 parameters and .set(\"alr\", \"yes\"))")
	}
	decls := declarationsForCandidate(prog, candidate.scopes)
	candidateName := candidate.name
	candidateFn := candidate.function

	params := map[string]bool{}
	if candidateFn.ParameterList != nil {
		for _, b := range candidateFn.ParameterList.List {
			if id, ok := b.Target.(*ast.Identifier); ok {
				params[id.Name.String()] = true
			}
		}
		if candidateFn.ParameterList.Rest != nil {
			if id, ok := candidateFn.ParameterList.Rest.(*ast.Identifier); ok {
				params[id.Name.String()] = true
			}
		}
	}
	locals := localsOf(candidateFn)

	refs := map[string]bool{}
	collectIdentsFromStmts(blockStmts(candidateFn.Body), refs)
	for p := range params {
		delete(refs, p)
	}
	for l := range locals {
		delete(refs, l)
	}
	for g := range jsGlobals {
		delete(refs, g)
	}
	delete(refs, candidateName)

	needed := map[string]bool{}
	queue := make([]string, 0, len(refs))
	origin := make(map[string]string, len(refs))
	for name := range refs {
		queue = append(queue, name)
		origin[name] = candidateName
	}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if needed[name] {
			continue
		}
		d, ok := decls[name]
		if !ok {
			return "", fmt.Errorf("nsig: dependency %q used by %q has no declaration to extract", name, origin[name])
		}
		if err := checkSafeDeclaration(d, name); err != nil {
			return "", fmt.Errorf("nsig: dependency %q of %q cannot be extracted safely: %w", name, candidateName, err)
		}
		needed[name] = true
		sub := map[string]bool{}
		if d.binding != nil {
			collectIdentsFromBinding(d.binding, sub)
		} else if d.expression != nil {
			collectIdentsFromExpression(d.expression, sub)
		} else {
			collectIdentsFromStmt(d.stmt, sub)
		}
		delete(sub, name)
		for g := range jsGlobals {
			delete(sub, g)
		}
		for s := range sub {
			if needed[s] {
				continue
			}
			if _, ok := decls[s]; ok {
				queue = append(queue, s)
				origin[s] = name
			} else {
				return "", fmt.Errorf("nsig: dependency %q used by %q has no declaration to extract", s, name)
			}
		}
	}

	type item struct {
		off  int
		text string
	}
	var items []item
	seen := map[string]bool{}
	emit := func(name string, declaration playerDeclaration) {
		if (declaration.stmt == nil && declaration.binding == nil) || seen[name] {
			return
		}
		seen[name] = true
		if declaration.binding != nil {
			text := "var " + sliceOf(source, declaration.binding) + ";"
			items = append(items, item{offsetOf(declaration.binding), text})
			return
		}
		items = append(items, item{offsetOf(declaration.stmt), sliceOf(source, declaration.stmt)})
	}
	for name := range needed {
		emit(name, decls[name])
	}
	if candidate.source != "" {
		items = append(items, item{offsetOf(candidate.function), candidate.source})
	} else {
		emit(candidateName, playerDeclaration{stmt: candidate.statement, binding: candidate.binding})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].off < items[j].off })
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, it.text)
	}
	parts = append(parts, "globalThis.__youtubeNSig = "+candidateName+";")
	return strings.Join(parts, "\n"), nil
}

type playerDeclaration struct {
	stmt       ast.Statement
	binding    *ast.Binding
	expression ast.Expression
}

type nsigCandidate struct {
	name      string
	function  *ast.FunctionLiteral
	statement ast.Statement
	binding   *ast.Binding
	source    string
	scopes    []*ast.FunctionLiteral
}

func findNSigCandidate(program *ast.Program) nsigCandidate {
	var candidate nsigCandidate
	seen := map[ast.Node]bool{}
	var visit func(reflect.Value, []*ast.FunctionLiteral)
	consider := func(name string, function *ast.FunctionLiteral, statement ast.Statement, binding *ast.Binding, source string, scopes []*ast.FunctionLiteral) {
		if candidate.function != nil || function == nil {
			return
		}
		if paramCount(function.ParameterList) >= 3 && funcMatchesNSig(function) {
			candidate = nsigCandidate{
				name: name, function: function, statement: statement, binding: binding, source: source,
				scopes: append([]*ast.FunctionLiteral(nil), scopes...),
			}
		}
	}
	visit = func(value reflect.Value, scopes []*ast.FunctionLiteral) {
		if !value.IsValid() {
			return
		}
		if value.Kind() == reflect.Interface {
			if value.IsNil() {
				return
			}
			visit(value.Elem(), scopes)
			return
		}
		if value.Kind() == reflect.Pointer {
			if value.IsNil() || !value.CanInterface() {
				return
			}
			if node, ok := value.Interface().(ast.Node); ok {
				if seen[node] {
					return
				}
				seen[node] = true
			}
			switch node := value.Interface().(type) {
			case *ast.Program:
				visit(reflect.ValueOf(node.Body), scopes)
			case *ast.FunctionLiteral:
				if node.Body != nil {
					nested := append(append([]*ast.FunctionLiteral(nil), scopes...), node)
					visit(reflect.ValueOf(node.Body), nested)
				}
			case *ast.Binding:
				if identifier, ok := node.Target.(*ast.Identifier); ok {
					if function, ok := node.Initializer.(*ast.FunctionLiteral); ok {
						consider(identifier.Name.String(), function, nil, node, "", scopes)
					}
				}
				visit(reflect.ValueOf(node.Initializer), scopes)
			case *ast.AssignExpression:
				if identifier, ok := node.Left.(*ast.Identifier); ok {
					if function, ok := node.Right.(*ast.FunctionLiteral); ok {
						source := identifier.Name.String() + " = " + function.Source + ";"
						consider(identifier.Name.String(), function, nil, nil, source, scopes)
					}
				}
				visit(reflect.ValueOf(node.Left), scopes)
				visit(reflect.ValueOf(node.Right), scopes)
			default:
				visitStructFields(value.Elem(), scopes, visit)
			}
			return
		}
		switch value.Kind() {
		case reflect.Slice, reflect.Array:
			for index := 0; index < value.Len(); index++ {
				visit(value.Index(index), scopes)
			}
		case reflect.Struct:
			if value.Type().PkgPath() == "github.com/dop251/goja/ast" {
				visitStructFields(value, scopes, visit)
			}
		}
	}
	visit(reflect.ValueOf(program), nil)
	return candidate
}

func visitStructFields(value reflect.Value, scopes []*ast.FunctionLiteral, visit func(reflect.Value, []*ast.FunctionLiteral)) {
	for index := 0; index < value.NumField(); index++ {
		field := value.Type().Field(index)
		if field.Name == "DeclarationList" || field.Name == "File" || field.Name == "Source" {
			continue
		}
		visit(value.Field(index), scopes)
	}
}

func declarationsForCandidate(program *ast.Program, scopes []*ast.FunctionLiteral) map[string]playerDeclaration {
	declarations := make(map[string]playerDeclaration)
	addStatement := func(statement ast.Statement) {
		switch declaration := statement.(type) {
		case *ast.VariableStatement:
			for _, binding := range declaration.List {
				if identifier, ok := binding.Target.(*ast.Identifier); ok && binding.Initializer != nil {
					declarations[identifier.Name.String()] = playerDeclaration{stmt: statement, binding: binding}
				}
			}
		case *ast.LexicalDeclaration:
			for _, binding := range declaration.List {
				if identifier, ok := binding.Target.(*ast.Identifier); ok && binding.Initializer != nil {
					declarations[identifier.Name.String()] = playerDeclaration{stmt: statement, binding: binding}
				}
			}
		case *ast.FunctionDeclaration:
			if declaration.Function != nil && declaration.Function.Name != nil {
				declarations[declaration.Function.Name.Name.String()] = playerDeclaration{stmt: statement}
			}
		case *ast.ExpressionStatement:
			assignment, ok := declaration.Expression.(*ast.AssignExpression)
			if !ok || !isSafeInit(assignment.Right) {
				return
			}
			if identifier, ok := assignment.Left.(*ast.Identifier); ok {
				declarations[identifier.Name.String()] = playerDeclaration{stmt: statement, expression: assignment.Right}
			}
		}
	}
	for _, statement := range program.Body {
		addStatement(statement)
	}
	for _, scope := range scopes {
		for _, declaration := range scope.DeclarationList {
			for _, binding := range declaration.List {
				if identifier, ok := binding.Target.(*ast.Identifier); ok {
					statement := &ast.VariableStatement{List: []*ast.Binding{binding}}
					declarations[identifier.Name.String()] = playerDeclaration{stmt: statement, binding: binding}
				}
			}
		}
		if scope.Body != nil {
			for _, statement := range scope.Body.List {
				addStatement(statement)
			}
		}
	}
	return declarations
}

func paramCount(pl *ast.ParameterList) int {
	if pl == nil {
		return 0
	}
	n := len(pl.List)
	if pl.Rest != nil {
		n++
	}
	return n
}

func funcMatchesNSig(fn *ast.FunctionLiteral) bool {
	if fn.Body == nil || fn.ParameterList == nil || len(fn.ParameterList.List) == 0 {
		return false
	}
	urlParam, ok := fn.ParameterList.List[0].Target.(*ast.Identifier)
	if !ok {
		return false
	}
	urlName := urlParam.Name.String()
	foundURL, foundSet := false, false
	markURL := func(e ast.Expression) {
		if n, ok := e.(*ast.NewExpression); ok && isConstructorExpression(n.Callee) {
			foundURL = true
		}
	}
	walkStmts(fn.Body.List, func(s ast.Statement) {
		switch t := s.(type) {
		case *ast.VariableStatement:
			for _, b := range t.List {
				if id, ok := b.Target.(*ast.Identifier); ok && id.Name.String() == "url" && b.Initializer != nil {
					markURL(b.Initializer)
				}
			}
		case *ast.LexicalDeclaration:
			for _, b := range t.List {
				if id, ok := b.Target.(*ast.Identifier); ok && id.Name.String() == "url" && b.Initializer != nil {
					markURL(b.Initializer)
				}
			}
		}
	}, func(e ast.Expression) {
		if a, ok := e.(*ast.AssignExpression); ok {
			if id, ok := a.Left.(*ast.Identifier); ok && id.Name.String() == urlName {
				markURL(a.Right)
			}
		}
		if c, ok := e.(*ast.CallExpression); ok && isAlrYesSet(c) {
			foundSet = true
		}
	})
	return foundURL && foundSet
}

func isConstructorExpression(expression ast.Expression) bool {
	switch expression.(type) {
	case *ast.Identifier, *ast.DotExpression, *ast.BracketExpression:
		return true
	default:
		return false
	}
}

func isAlrYesSet(c *ast.CallExpression) bool {
	var method string
	switch cal := c.Callee.(type) {
	case *ast.DotExpression:
		method = cal.Identifier.Name.String()
	case *ast.BracketExpression:
		if s, ok := cal.Member.(*ast.StringLiteral); ok {
			method = s.Value.String()
		}
	default:
		return false
	}
	if method != "set" || len(c.ArgumentList) < 2 {
		return false
	}
	a0, ok0 := c.ArgumentList[0].(*ast.StringLiteral)
	a1, ok1 := c.ArgumentList[1].(*ast.StringLiteral)
	return ok0 && ok1 && a0.Value.String() == "alr" && a1.Value.String() == "yes"
}

// checkSafeBinding returns an error if the named binding's initializer in
// stmt has potential side effects (call/new expressions at the top level).
func checkSafeBinding(stmt ast.Statement, name string) error {
	switch s := stmt.(type) {
	case *ast.VariableStatement:
		for _, b := range s.List {
			id, ok := b.Target.(*ast.Identifier)
			if !ok || id.Name.String() != name {
				continue
			}
			if b.Initializer == nil {
				return fmt.Errorf("binding has no initializer")
			}
			if !isSafeInit(b.Initializer) {
				return fmt.Errorf("initializer has side-effectful expression %T", b.Initializer)
			}
			return nil
		}
		return fmt.Errorf("binding not found")
	case *ast.LexicalDeclaration:
		for _, b := range s.List {
			id, ok := b.Target.(*ast.Identifier)
			if !ok || id.Name.String() != name {
				continue
			}
			if b.Initializer == nil {
				return fmt.Errorf("binding has no initializer")
			}
			if !isSafeInit(b.Initializer) {
				return fmt.Errorf("initializer has side-effectful expression %T", b.Initializer)
			}
			return nil
		}
		return fmt.Errorf("binding not found")
	case *ast.FunctionDeclaration:
		return nil
	default:
		return fmt.Errorf("unsupported declaration kind %T", stmt)
	}
}

func checkSafeDeclaration(declaration playerDeclaration, name string) error {
	if declaration.binding != nil {
		if declaration.binding.Initializer == nil {
			return fmt.Errorf("binding %q has no initializer", name)
		}
		if !isSafeInit(declaration.binding.Initializer) {
			return fmt.Errorf("initializer has side-effectful expression %T", declaration.binding.Initializer)
		}
		return nil
	}
	if declaration.expression != nil {
		if !isSafeInit(declaration.expression) {
			return fmt.Errorf("assignment has side-effectful expression %T", declaration.expression)
		}
		return nil
	}
	return checkSafeBinding(declaration.stmt, name)
}

func isSafeInit(e ast.Expression) bool {
	switch t := e.(type) {
	case *ast.FunctionLiteral, *ast.ArrowFunctionLiteral,
		*ast.StringLiteral, *ast.NumberLiteral, *ast.BooleanLiteral,
		*ast.NullLiteral, *ast.RegExpLiteral:
		return true
	case *ast.Identifier:
		return true
	case *ast.ArrayLiteral:
		for _, v := range t.Value {
			if v == nil {
				continue
			}
			if !isSafeInit(v) {
				return false
			}
		}
		return true
	case *ast.ObjectLiteral:
		for _, p := range t.Value {
			switch pr := p.(type) {
			case *ast.PropertyShort:
				if pr.Initializer != nil {
					if !isSafeInit(pr.Initializer) {
						return false
					}
				}
			case *ast.PropertyKeyed:
				if pr.Computed && !isSafeInit(pr.Key) {
					return false
				}
				if !isSafeInit(pr.Value) {
					return false
				}
			default:
				return false
			}
		}
		return true
	case *ast.UnaryExpression:
		if t.Postfix {
			return false
		}
		switch t.Operator.String() {
		case "!", "+", "-", "~", "typeof", "void":
			return isSafeInit(t.Operand)
		default:
			return false
		}
	case *ast.BinaryExpression:
		return isSafeInit(t.Left) && isSafeInit(t.Right)
	case *ast.ConditionalExpression:
		return isSafeInit(t.Test) && isSafeInit(t.Consequent) && isSafeInit(t.Alternate)
	case *ast.CallExpression:
		callee, ok := t.Callee.(*ast.DotExpression)
		if !ok || callee.Identifier.Name.String() != "split" {
			return false
		}
		if _, ok := callee.Left.(*ast.StringLiteral); !ok {
			return false
		}
		for _, argument := range t.ArgumentList {
			if !isSafeInit(argument) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

var jsGlobals = map[string]bool{
	"URL": true, "URLSearchParams": true, "Object": true, "Array": true,
	"Math": true, "String": true, "Number": true, "Boolean": true,
	"JSON": true, "RegExp": true, "Date": true, "Error": true,
	"TypeError": true, "ReferenceError": true, "RangeError": true,
	"URIError": true, "EvalError": true, "AggregateError": true,
	"Promise": true, "Map": true, "Set": true, "WeakMap": true,
	"WeakSet": true, "Symbol": true, "BigInt": true, "Intl": true,
	"Reflect": true, "Proxy": true, "Function": true,
	"parseInt": true, "parseFloat": true, "isFinite": true, "isNaN": true,
	"undefined": true, "NaN": true, "Infinity": true, "globalThis": true,
	"encodeURI": true, "decodeURI": true, "encodeURIComponent": true,
	"decodeURIComponent": true, "escape": true, "unescape": true,
	"g": true,
}

func blockStmts(b *ast.BlockStatement) []ast.Statement {
	if b == nil {
		return nil
	}
	return b.List
}

func localsOf(fn *ast.FunctionLiteral) map[string]bool {
	out := map[string]bool{}
	if fn.Body == nil {
		return out
	}
	collectLocalNames(fn.Body.List, out)
	return out
}

func collectLocalNames(statements []ast.Statement, out map[string]bool) {
	for _, statement := range statements {
		if isNilASTNode(statement) {
			continue
		}
		switch node := statement.(type) {
		case *ast.BlockStatement:
			collectLocalNames(node.List, out)
		case *ast.VariableStatement:
			collectBindingNames(node.List, out)
		case *ast.LexicalDeclaration:
			collectBindingNames(node.List, out)
		case *ast.FunctionDeclaration:
			if node.Function != nil && node.Function.Name != nil {
				out[node.Function.Name.Name.String()] = true
			}
		case *ast.IfStatement:
			collectLocalNames([]ast.Statement{node.Consequent}, out)
			if node.Alternate != nil {
				collectLocalNames([]ast.Statement{node.Alternate}, out)
			}
		case *ast.ForStatement:
			switch initializer := node.Initializer.(type) {
			case *ast.ForLoopInitializerVarDeclList:
				collectBindingNames(initializer.List, out)
			case *ast.ForLoopInitializerLexicalDecl:
				collectBindingNames(initializer.LexicalDeclaration.List, out)
			}
			collectLocalNames([]ast.Statement{node.Body}, out)
		case *ast.ForInStatement:
			switch into := node.Into.(type) {
			case *ast.ForIntoVar:
				if identifier, ok := into.Binding.Target.(*ast.Identifier); ok {
					out[identifier.Name.String()] = true
				}
			case *ast.ForDeclaration:
				if identifier, ok := into.Target.(*ast.Identifier); ok {
					out[identifier.Name.String()] = true
				}
			}
			collectLocalNames([]ast.Statement{node.Body}, out)
		case *ast.WhileStatement:
			collectLocalNames([]ast.Statement{node.Body}, out)
		case *ast.DoWhileStatement:
			collectLocalNames([]ast.Statement{node.Body}, out)
		case *ast.TryStatement:
			collectLocalNames([]ast.Statement{node.Body}, out)
			if node.Catch != nil {
				if identifier, ok := node.Catch.Parameter.(*ast.Identifier); ok {
					out[identifier.Name.String()] = true
				}
				collectLocalNames([]ast.Statement{node.Catch.Body}, out)
			}
			if node.Finally != nil {
				collectLocalNames([]ast.Statement{node.Finally}, out)
			}
		case *ast.LabelledStatement:
			collectLocalNames([]ast.Statement{node.Statement}, out)
		case *ast.SwitchStatement:
			for _, branch := range node.Body {
				collectLocalNames(branch.Consequent, out)
			}
		case *ast.WithStatement:
			collectLocalNames([]ast.Statement{node.Body}, out)
		}
	}
}

func collectBindingNames(bindings []*ast.Binding, out map[string]bool) {
	for _, binding := range bindings {
		if identifier, ok := binding.Target.(*ast.Identifier); ok {
			out[identifier.Name.String()] = true
		}
	}
}

// --- source slicing (file.Idx is 1-based; 0 means NoIdx) ---

func offsetOf(n ast.Node) int {
	return int(n.Idx0()) - 1
}

func sliceOf(source []byte, n ast.Node) string {
	start := int(n.Idx0()) - 1
	end := int(n.Idx1()) - 1
	if start < 0 {
		start = 0
	}
	if end > len(source) {
		end = len(source)
	}
	if start > end {
		start = end
	}
	return string(source[start:end])
}

// --- traversal ---

func walkStmts(list []ast.Statement, preStmt func(ast.Statement), preExpr func(ast.Expression)) {
	for _, s := range list {
		walkStmt(s, preStmt, preExpr)
	}
}

func walkStmt(s ast.Statement, preStmt func(ast.Statement), preExpr func(ast.Expression)) {
	if s == nil {
		return
	}
	if preStmt != nil {
		preStmt(s)
	}
	switch t := s.(type) {
	case *ast.BlockStatement:
		walkStmts(t.List, preStmt, preExpr)
	case *ast.ExpressionStatement:
		walkExpr(t.Expression, preExpr)
	case *ast.VariableStatement:
		for _, b := range t.List {
			if b.Initializer != nil {
				walkExpr(b.Initializer, preExpr)
			}
		}
	case *ast.LexicalDeclaration:
		for _, b := range t.List {
			if b.Initializer != nil {
				walkExpr(b.Initializer, preExpr)
			}
		}
	case *ast.ReturnStatement:
		if t.Argument != nil {
			walkExpr(t.Argument, preExpr)
		}
	case *ast.IfStatement:
		walkExpr(t.Test, preExpr)
		walkStmt(t.Consequent, preStmt, preExpr)
		if t.Alternate != nil {
			walkStmt(t.Alternate, preStmt, preExpr)
		}
	case *ast.ForStatement:
		walkStmt(t.Body, preStmt, preExpr)
		if t.Test != nil {
			walkExpr(t.Test, preExpr)
		}
		if t.Update != nil {
			walkExpr(t.Update, preExpr)
		}
	case *ast.WhileStatement:
		walkExpr(t.Test, preExpr)
		walkStmt(t.Body, preStmt, preExpr)
	case *ast.TryStatement:
		if t.Body != nil {
			walkStmt(t.Body, preStmt, preExpr)
		}
		if t.Catch != nil && t.Catch.Body != nil {
			walkStmt(t.Catch.Body, preStmt, preExpr)
		}
		if t.Finally != nil {
			walkStmt(t.Finally, preStmt, preExpr)
		}
	case *ast.FunctionDeclaration:
		if t.Function != nil && t.Function.Body != nil {
			walkStmts(t.Function.Body.List, preStmt, preExpr)
		}
	case *ast.LabelledStatement:
		walkStmt(t.Statement, preStmt, preExpr)
	case *ast.SwitchStatement:
		walkExpr(t.Discriminant, preExpr)
		for _, c := range t.Body {
			if c.Test != nil {
				walkExpr(c.Test, preExpr)
			}
			walkStmts(c.Consequent, preStmt, preExpr)
		}
	}
}

func walkExpr(e ast.Expression, pre func(ast.Expression)) {
	if e == nil {
		return
	}
	if pre != nil {
		pre(e)
	}
	switch t := e.(type) {
	case *ast.AssignExpression:
		walkExpr(t.Left, pre)
		walkExpr(t.Right, pre)
	case *ast.CallExpression:
		walkExpr(t.Callee, pre)
		for _, a := range t.ArgumentList {
			walkExpr(a, pre)
		}
	case *ast.NewExpression:
		walkExpr(t.Callee, pre)
		for _, a := range t.ArgumentList {
			walkExpr(a, pre)
		}
	case *ast.DotExpression:
		walkExpr(t.Left, pre)
	case *ast.BracketExpression:
		walkExpr(t.Left, pre)
		walkExpr(t.Member, pre)
	case *ast.FunctionLiteral:
		if t.Body != nil {
			walkStmts(t.Body.List, nil, pre)
		}
	case *ast.ArrowFunctionLiteral:
		switch b := t.Body.(type) {
		case *ast.BlockStatement:
			walkStmts(b.List, nil, pre)
		case *ast.ExpressionBody:
			walkExpr(b.Expression, pre)
		}
	case *ast.BinaryExpression:
		walkExpr(t.Left, pre)
		walkExpr(t.Right, pre)
	case *ast.UnaryExpression:
		walkExpr(t.Operand, pre)
	case *ast.ConditionalExpression:
		walkExpr(t.Test, pre)
		walkExpr(t.Consequent, pre)
		walkExpr(t.Alternate, pre)
	case *ast.SequenceExpression:
		for _, x := range t.Sequence {
			walkExpr(x, pre)
		}
	case *ast.ArrayLiteral:
		for _, v := range t.Value {
			walkExpr(v, pre)
		}
	case *ast.ObjectLiteral:
		for _, p := range t.Value {
			switch pr := p.(type) {
			case *ast.PropertyShort:
				if pr.Initializer != nil {
					walkExpr(pr.Initializer, pre)
				}
			case *ast.PropertyKeyed:
				walkExpr(pr.Value, pre)
			}
		}
	case *ast.TemplateLiteral:
		for _, x := range t.Expressions {
			walkExpr(x, pre)
		}
		if t.Tag != nil {
			walkExpr(t.Tag, pre)
		}
	}
}

func collectIdentsFromStmts(list []ast.Statement, out map[string]bool) {
	for _, s := range list {
		collectIdentsFromStmt(s, out)
	}
}

func collectIdentsFromStmt(s ast.Statement, out map[string]bool) {
	if isNilASTNode(s) {
		return
	}
	switch node := s.(type) {
	case *ast.BlockStatement:
		collectIdentsFromStmts(node.List, out)
	case *ast.ExpressionStatement:
		collectIdentsFromExpression(node.Expression, out)
	case *ast.VariableStatement:
		for _, binding := range node.List {
			collectIdentsFromExpression(binding.Initializer, out)
		}
	case *ast.LexicalDeclaration:
		for _, binding := range node.List {
			collectIdentsFromExpression(binding.Initializer, out)
		}
	case *ast.ReturnStatement:
		collectIdentsFromExpression(node.Argument, out)
	case *ast.ThrowStatement:
		collectIdentsFromExpression(node.Argument, out)
	case *ast.IfStatement:
		collectIdentsFromExpression(node.Test, out)
		collectIdentsFromStmt(node.Consequent, out)
		collectIdentsFromStmt(node.Alternate, out)
	case *ast.ForStatement:
		switch initializer := node.Initializer.(type) {
		case *ast.ForLoopInitializerExpression:
			collectIdentsFromExpression(initializer.Expression, out)
		case *ast.ForLoopInitializerVarDeclList:
			for _, binding := range initializer.List {
				collectIdentsFromExpression(binding.Initializer, out)
			}
		case *ast.ForLoopInitializerLexicalDecl:
			for _, binding := range initializer.LexicalDeclaration.List {
				collectIdentsFromExpression(binding.Initializer, out)
			}
		}
		collectIdentsFromExpression(node.Test, out)
		collectIdentsFromExpression(node.Update, out)
		collectIdentsFromStmt(node.Body, out)
	case *ast.ForInStatement:
		if into, ok := node.Into.(*ast.ForIntoExpression); ok {
			collectIdentsFromExpression(into.Expression, out)
		}
		collectIdentsFromExpression(node.Source, out)
		collectIdentsFromStmt(node.Body, out)
	case *ast.WhileStatement:
		collectIdentsFromExpression(node.Test, out)
		collectIdentsFromStmt(node.Body, out)
	case *ast.DoWhileStatement:
		collectIdentsFromStmt(node.Body, out)
		collectIdentsFromExpression(node.Test, out)
	case *ast.TryStatement:
		collectIdentsFromStmt(node.Body, out)
		if node.Catch != nil {
			catchRefs := map[string]bool{}
			collectIdentsFromStmt(node.Catch.Body, catchRefs)
			if identifier, ok := node.Catch.Parameter.(*ast.Identifier); ok {
				delete(catchRefs, identifier.Name.String())
			}
			mergeIdentifiers(out, catchRefs)
		}
		collectIdentsFromStmt(node.Finally, out)
	case *ast.FunctionDeclaration:
		collectFunctionIdentifiers(node.Function, out)
	case *ast.LabelledStatement:
		collectIdentsFromStmt(node.Statement, out)
	case *ast.SwitchStatement:
		collectIdentsFromExpression(node.Discriminant, out)
		for _, branch := range node.Body {
			collectIdentsFromExpression(branch.Test, out)
			collectIdentsFromStmts(branch.Consequent, out)
		}
	case *ast.WithStatement:
		collectIdentsFromExpression(node.Object, out)
		collectIdentsFromStmt(node.Body, out)
	}
}

func collectIdentsFromBinding(binding *ast.Binding, out map[string]bool) {
	if binding == nil || binding.Initializer == nil {
		return
	}
	collectIdentsFromExpression(binding.Initializer, out)
}

func collectIdentsFromExpression(expression ast.Expression, out map[string]bool) {
	if isNilASTNode(expression) {
		return
	}
	switch node := expression.(type) {
	case *ast.Identifier:
		out[node.Name.String()] = true
	case *ast.FunctionLiteral:
		collectFunctionIdentifiers(node, out)
	case *ast.ArrowFunctionLiteral:
		refs := map[string]bool{}
		switch body := node.Body.(type) {
		case *ast.BlockStatement:
			collectIdentsFromStmts(body.List, refs)
			for name := range localsOf(&ast.FunctionLiteral{Body: body}) {
				delete(refs, name)
			}
		case *ast.ExpressionBody:
			collectIdentsFromExpression(body.Expression, refs)
		}
		deleteParameterNames(node.ParameterList, refs)
		mergeIdentifiers(out, refs)
	case *ast.AssignExpression:
		collectIdentsFromExpression(node.Left, out)
		collectIdentsFromExpression(node.Right, out)
	case *ast.CallExpression:
		collectIdentsFromExpression(node.Callee, out)
		for _, argument := range node.ArgumentList {
			collectIdentsFromExpression(argument, out)
		}
	case *ast.NewExpression:
		collectIdentsFromExpression(node.Callee, out)
		for _, argument := range node.ArgumentList {
			collectIdentsFromExpression(argument, out)
		}
	case *ast.DotExpression:
		collectIdentsFromExpression(node.Left, out)
	case *ast.BracketExpression:
		collectIdentsFromExpression(node.Left, out)
		collectIdentsFromExpression(node.Member, out)
	case *ast.BinaryExpression:
		collectIdentsFromExpression(node.Left, out)
		collectIdentsFromExpression(node.Right, out)
	case *ast.UnaryExpression:
		collectIdentsFromExpression(node.Operand, out)
	case *ast.ConditionalExpression:
		collectIdentsFromExpression(node.Test, out)
		collectIdentsFromExpression(node.Consequent, out)
		collectIdentsFromExpression(node.Alternate, out)
	case *ast.SequenceExpression:
		for _, item := range node.Sequence {
			collectIdentsFromExpression(item, out)
		}
	case *ast.ArrayLiteral:
		for _, item := range node.Value {
			collectIdentsFromExpression(item, out)
		}
	case *ast.ObjectLiteral:
		for _, property := range node.Value {
			switch item := property.(type) {
			case *ast.PropertyShort:
				if item.Initializer != nil {
					collectIdentsFromExpression(item.Initializer, out)
				} else {
					out[item.Name.Name.String()] = true
				}
			case *ast.PropertyKeyed:
				if item.Computed {
					collectIdentsFromExpression(item.Key, out)
				}
				collectIdentsFromExpression(item.Value, out)
			}
		}
	case *ast.TemplateLiteral:
		collectIdentsFromExpression(node.Tag, out)
		for _, item := range node.Expressions {
			collectIdentsFromExpression(item, out)
		}
	case *ast.AwaitExpression:
		collectIdentsFromExpression(node.Argument, out)
	case *ast.YieldExpression:
		collectIdentsFromExpression(node.Argument, out)
	}
}

func isNilASTNode(node ast.Node) bool {
	if node == nil {
		return true
	}
	value := reflect.ValueOf(node)
	return value.Kind() == reflect.Pointer && value.IsNil()
}

func collectFunctionIdentifiers(function *ast.FunctionLiteral, out map[string]bool) {
	if function == nil {
		return
	}
	refs := map[string]bool{}
	if function.Body != nil {
		collectIdentsFromStmts(function.Body.List, refs)
		for name := range localsOf(function) {
			delete(refs, name)
		}
	}
	deleteParameterNames(function.ParameterList, refs)
	if function.Name != nil {
		delete(refs, function.Name.Name.String())
	}
	mergeIdentifiers(out, refs)
}

func deleteParameterNames(parameters *ast.ParameterList, identifiers map[string]bool) {
	if parameters == nil {
		return
	}
	for _, parameter := range parameters.List {
		if identifier, ok := parameter.Target.(*ast.Identifier); ok {
			delete(identifiers, identifier.Name.String())
		}
	}
	if identifier, ok := parameters.Rest.(*ast.Identifier); ok {
		delete(identifiers, identifier.Name.String())
	}
}

func mergeIdentifiers(destination, source map[string]bool) {
	for identifier := range source {
		destination[identifier] = true
	}
}
