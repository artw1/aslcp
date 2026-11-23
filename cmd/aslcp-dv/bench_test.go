package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestBenchFake(t *testing.T) {
	var out, errb bytes.Buffer
	if rc := dispatch(context.Background(), []string{"bench", "-fake", "-frames", "200"}, &out, &errb); rc != 0 {
		t.Fatalf("rc=%d\n%s%s", rc, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "PASS") || !strings.Contains(out.String(), "timeouts   0") {
		t.Fatal(out.String())
	}
}

func TestBenchModesAndFlags(t *testing.T) {
	for _, m := range []string{"encode", "decode"} {
		var out, errb bytes.Buffer
		if rc := dispatch(context.Background(), []string{"bench", "-fake", "-frames", "40", "-mode", m, "-concurrency", "2"}, &out, &errb); rc != 0 {
			t.Fatalf("%s rc=%d %s", m, rc, errb.String())
		}
	}
	var out, errb bytes.Buffer
	if rc := dispatch(context.Background(), []string{"bench", "-mode", "bogus"}, &out, &errb); rc != 2 {
		t.Fatalf("rc=%d", rc)
	}
}

func TestVersionAndUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if rc := dispatch(context.Background(), []string{"version"}, &out, &errb); rc != 0 || !strings.Contains(out.String(), "aslcp-dv") {
		t.Fatal(rc, out.String())
	}
	if rc := dispatch(context.Background(), nil, &out, &errb); rc != 2 {
		t.Fatal(rc)
	}
	if rc := dispatch(context.Background(), []string{"nope"}, &out, &errb); rc != 2 {
		t.Fatal(rc)
	}
}
