package main

import "testing"

func TestCanonicalPreservesContentAcrossLineBreaks(t *testing.T) {
	if canonical("国家7万亿，先留养老钱。") != canonical("国家7万亿 \n先留养老钱 \n") {
		t.Fatal("layout-only changes should match")
	}
	if canonical("养老钱7.5万") == canonical("养老钱75万") {
		t.Fatal("decimal change must not pass")
	}
	if canonical("7万亿") == canonical("7亿元") {
		t.Fatal("unit change must not pass")
	}
	if canonical("少花钱") == canonical("花钱") {
		t.Fatal("omitted content must not pass")
	}
	if canonical("5G基站") != canonical("5G基站\n") {
		t.Fatal("technical terms must survive")
	}
}
