package twitch

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Los tests de DownloadClip ejecutan un binario falso con los mismos argumentos
// que el adapter real: `clipdownload -u <clipID> -o <dest>`. En Unix el fake es
// un script sh ejecutable; en Windows un .bat (exec.Command lo lanza vía cmd.exe
// porque CreateProcess resuelve las extensiones de PATHEXT).
//
// Los snippets de batch evitan paréntesis en el parseo de args: `%1`/`%2` se
// expanden al leer el bloque completo, así que el recorrido va con goto.

// batFindOut recorre los args hasta el -o y deja la ruta de salida en %out%.
const batFindOut = "@echo off\r\n" +
	"set out=\r\n" +
	":loop\r\n" +
	"if \"%1\"==\"\" goto end\r\n" +
	"if \"%1\"==\"-o\" goto got\r\n" +
	"shift\r\n" +
	"goto loop\r\n" +
	":got\r\n" +
	"set out=%2\r\n" +
	"shift\r\n" +
	"shift\r\n" +
	"goto loop\r\n" +
	":end\r\n"

// shFindOut hace lo mismo en sh: deja la ruta de salida en $out.
const shFindOut = "out=\"\"\n" +
	"while [ $# -gt 0 ]; do case \"$1\" in -o) out=\"$2\"; shift 2;; *) shift;; esac; done\n"

// fakeDownloader escribe un ejecutable falso en dir y devuelve la ruta a ejecutar
// (con extensión .bat en Windows).
func fakeDownloader(t *testing.T, dir string, name string, unixScript string, windowsScript string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	body := unixScript
	if runtime.GOOS == "windows" {
		path += ".bat"
		body = windowsScript
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake downloader %s: %v", path, err)
	}
	return path
}

// fakeDownloaderOK crea el archivo de salida con contenido (descarga exitosa).
func fakeDownloaderOK(t *testing.T, dir string) string {
	t.Helper()
	return fakeDownloader(t, dir, "fake-downloader",
		"#!/bin/sh\n"+shFindOut+"printf 'FAKE_VIDEO_CONTENT' > \"$out\"\n",
		batFindOut+"<nul set /p=\"FAKE_VIDEO_CONTENT\">\"%out%\"\r\nexit /b 0\r\n")
}

// fakeDownloaderFailing escribe a stderr y sale con código 1 (clip inexistente).
func fakeDownloaderFailing(t *testing.T, dir string) string {
	t.Helper()
	return fakeDownloader(t, dir, "fake-downloader-fail",
		"#!/bin/sh\necho 'FATAL: clip not found' >&2\nexit 1\n",
		"@echo off\r\necho FATAL: clip not found 1>&2\r\nexit /b 1\r\n")
}

// fakeDownloaderEmpty sale 0 pero deja el archivo de salida vacío.
func fakeDownloaderEmpty(t *testing.T, dir string) string {
	t.Helper()
	return fakeDownloader(t, dir, "fake-downloader-empty",
		"#!/bin/sh\n"+shFindOut+": > \"$out\"\n",
		batFindOut+"type nul >\"%out%\"\r\nexit /b 0\r\n")
}

// fakeDownloaderSlow tarda ~5s: sirve para cortar el proceso con el contexto.
func fakeDownloaderSlow(t *testing.T, dir string) string {
	t.Helper()
	return fakeDownloader(t, dir, "fake-downloader-slow",
		"#!/bin/sh\nsleep 5\n",
		"@echo off\r\nping -n 6 127.0.0.1 > nul\r\n")
}
