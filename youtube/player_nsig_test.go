package youtube

import (
	"strings"
	"testing"

	"github.com/dop251/goja"
	"github.com/dop251/goja/parser"
)

const nsigFixture = `
var unrelated = function(x) { return x + 1; };
var helperB = function(s) { return s.split("").reverse().join(""); };
var helperA = function(s) { return helperB(s) + "!"; };
var sideEffect = initSomething();
var NSA = function(a, b, c, d) {
	var url = new URL(a);
	url.searchParams.set('alr', 'yes');
	return helperA(url.searchParams.get('n')) + c;
};
`

func TestExtractNSigCurrentStyle(t *testing.T) {
	out, err := extractNSigScript([]byte(nsigFixture))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !strings.Contains(out, "NSA") {
		t.Fatalf("output missing candidate NSA:\n%s", out)
	}
	if !strings.Contains(out, "helperA") || !strings.Contains(out, "helperB") {
		t.Fatalf("output missing transitive helpers:\n%s", out)
	}
}

func TestExtractNSigNestedIIFEAndAssignedDependencies(t *testing.T) {
	source := []byte(`(function(){
	var reverse=function(value){return value.split("").reverse().join("")};
	(function(){
		var helper;
		helper=function(value){return reverse(value)};
		NSig=function(url,sp,sig){url=new g.$R(url,!0);if(sig)url.set(sp,helper(sig));url.set("alr","yes")};
	})();
})();`)
	output, err := extractNSigScript(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"reverse", "helper=function", "NSig = function", "globalThis.__youtubeNSig = NSig"} {
		if !strings.Contains(output, want) {
			t.Errorf("extracted script missing %q:\n%s", want, output)
		}
	}
	vm := goja.New()
	if _, err := vm.RunString(output); err != nil {
		t.Fatalf("run extracted script: %v", err)
	}
}

func TestExtractNSigIgnoresUnrelated(t *testing.T) {
	out, err := extractNSigScript([]byte(nsigFixture))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if strings.Contains(out, "unrelated") {
		t.Fatalf("output should not include unrelated function:\n%s", out)
	}
	if strings.Contains(out, "sideEffect") {
		t.Fatalf("output must not include side-effectful initializer:\n%s", out)
	}
	// Output must parse and define the candidate as callable.
	vm := goja.New()
	if _, err := vm.RunString(out + "\nthis.__n = NSA;"); err != nil {
		t.Fatalf("run extracted script: %v", err)
	}
	if _, err := parser.ParseFile(nil, "out.js", out, 0); err != nil {
		t.Fatalf("extracted script does not parse: %v", err)
	}
}

func TestExtractNSigUnsafeDependencyErrors(t *testing.T) {
	src := `
var dep = makeThing();
var NSB = function(a, b, c) {
	var url = new URL(a);
	url.searchParams.set('alr', 'yes');
	return dep(a);
};
`
	if _, err := extractNSigScript([]byte(src)); err == nil {
		t.Fatal("expected error for side-effectful dependency, got nil")
	}
}

func TestExtractNSigNoMatch(t *testing.T) {
	src := `var a = function(x) { return x; };`
	if _, err := extractNSigScript([]byte(src)); err == nil {
		t.Fatal("expected error on no match, got nil")
	}
}
