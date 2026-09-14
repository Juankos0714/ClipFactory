// Separación específica de plataforma (build tags).
//
// Los test de internal/db mockean la eliminación de archivos (osRemove) para
// probar CleanupOldCompletedVideos sin tocar el disco. En Go, os.Remove existe
// en todos los sistemas operativos, así que esta variante "real" vive acá y el
// init() la conecta al puntero osRemove declarado en models.go.
//
// ¿Por qué un puntero y no llamar os.Remove directo? Para poder reemplazarlo en
// tests sin hooks ni interfaces extra (ver CleanupOldCompletedVideos).
package db

import (
	"os"
)

// init conecta la implementación real de osRemove al arrancar el paquete.
// Los tests pueden reasignar osRemove a un mock antes de ejecutar Cleanup.
func init() {
	osRemove = osRemoveReal
}

// osRemoveReal es la implementación real: delega en os.Remove estándar.
func osRemoveReal(path string) error {
	return os.Remove(path)
}
