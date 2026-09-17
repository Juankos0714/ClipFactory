package db

// Este archivo implementa las consultas agregadas para el comando 'status'
// del CLI: conteos por estado de cada tabla del pipeline. Son lecturas puras
// (GROUP BY COUNT), sin escrituras, seguras de correr con el worker en marcha
// gracias a WAL (los lectores no bloquean al escritor ni al revés).
//
// Convención de todos los conteos:
//   - COUNT(*) siempre retorna una fila por grupo (0 si no hay filas), así que
//     no hay casos "no existe": un grupo ausente = 0.
//   - Los mapas devueltos son SIEMPRE no-nil: más cómodo para el llamador.

import (
	"database/sql"
	"fmt"
)

// PublicationKeysForClip devuelve los marcadores deterministas de publicación
// para un clip: "cf-<clipID>-<videoID>" y "cf-<clipID>". Es la CLAVE DE
// RECONCILIACIÓN anti-duplicados: se inserta en la descripción/detalle del
// video publicado y, antes de reintentar un upload, el publisher la busca
// entre los videos publicados recientemente en la plataforma. Si aparece, el
// upload YA SUCEDIÓ (crash entre upload y update de DB) y NO se repite.
//
// Determinista: no depende del reloj ni del orden de reintentos, así que el
// reintento encuentra lo que subió el intento anterior, sin importar cuándo.// Incluye videoID (y no solo clipID) porque un mismo clip puede re-procesarse
// y generar otra fila de video; el marcador con ambos identifica exactamente
// el artefacto publicado.
func PublicationKeysForClip(clipID, videoID int64) []string {
	return []string{
		fmt.Sprintf("cf-%d-%d", clipID, videoID),
		fmt.Sprintf("cf-%d", clipID),
	}
}

// GetVideoIDByClipID devuelve el video de origen de un clip (getter liviano
// para armar los marcadores de reconciliación). 0 si no existe.
func GetVideoIDByClipID(db *sql.DB, clipID int64) (int64, error) {
	var videoID int64
	err := db.QueryRow(`SELECT video_id FROM clips WHERE id = ?`, clipID).Scan(&videoID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return videoID, err
}

// GetSourceStats devuelve la cantidad de canales por plataforma, separados
// entre activos (el discovery los consulta) e inactivos (pausados).
//
// La clave es la plataforma (ej: "twitch", "kick"); plataformas sin canales
// no aparecen en el mapa.
func GetSourceStats(db *sql.DB) (map[string]SourceCounts, error) {
	rows, err := db.Query(
		`SELECT platform,
		        SUM(CASE WHEN active = 1 THEN 1 ELSE 0 END) AS activos,
		        SUM(CASE WHEN active = 0 THEN 1 ELSE 0 END) AS inactivos
		 FROM sources GROUP BY platform`,
	)
	if err != nil {
		return nil, fmt.Errorf("source stats: %w", err)
	}
	defer rows.Close()

	out := make(map[string]SourceCounts)
	for rows.Next() {
		var platform string
		var c SourceCounts
		if err := rows.Scan(&platform, &c.Active, &c.Inactive); err != nil {
			return nil, fmt.Errorf("scan source stats: %w", err)
		}
		out[platform] = c
	}
	return out, rows.Err()
}

// SourceCounts agrupa los canales de una plataforma por su estado.
type SourceCounts struct {
	Active   int // active = 1: el discovery los consulta
	Inactive int // active = 0: pausados
}

// GetSourceClipStats devuelve la cantidad de source_clips por plataforma y
// estado (detected, downloaded, skipped, error). Muestra qué está haciendo el
// lado "origen" del pipeline (Twitch/Kick) antes de que los archivos toquen
// disco local.
func GetSourceClipStats(db *sql.DB) (map[string]map[string]int, error) {
	rows, err := db.Query(
		`SELECT platform, status, COUNT(*) FROM source_clips GROUP BY platform, status`,
	)
	if err != nil {
		return nil, fmt.Errorf("source_clip stats: %w", err)
	}
	defer rows.Close()

	out := make(map[string]map[string]int)
	for rows.Next() {
		var platform, status string
		var n int
		if err := rows.Scan(&platform, &status, &n); err != nil {
			return nil, fmt.Errorf("scan source_clip stats: %w", err)
		}
		if out[platform] == nil {
			out[platform] = make(map[string]int)
		}
		out[platform][status] = n
	}
	return out, rows.Err()
}

// GetVideoStats devuelve la cantidad de videos por estado
// (incoming, processing, completed, failed).
func GetVideoStats(db *sql.DB) (map[string]int, error) {
	return countByStatus(db, "videos")
}

// GetClipStats devuelve la cantidad de clips por estado
// (processing, completed, failed).
func GetClipStats(db *sql.DB) (map[string]int, error) {
	return countByStatus(db, "clips")
}

// countByStatus es el helper común para las tablas con columna status simple
// (videos, clips): un GROUP BY status con COUNT. Los nombres de tabla vienen
// de constantes del paquete (nunca de input del usuario), por eso el
// formateo directo es seguro.
func countByStatus(db *sql.DB, table string) (map[string]int, error) {
	// lista blanca: solo estas tablas se aceptan (defensivo, aunque hoy el
	// único caller pasa literales)
	switch table {
	case "videos", "clips":
		// ok
	default:
		return nil, fmt.Errorf("tabla no soportada para stats: %s", table)
	}

	rows, err := db.Query(fmt.Sprintf(`SELECT status, COUNT(*) FROM %s GROUP BY status`, table))
	if err != nil {
		return nil, fmt.Errorf("%s stats: %w", table, err)
	}
	defer rows.Close()

	out := make(map[string]int)
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, fmt.Errorf("scan %s stats: %w", table, err)
		}
		out[status] = n
	}
	return out, rows.Err()
}

// GetPublicationStats devuelve la cantidad de publications por plataforma y
// estado (pending, published, error, waiting_rate_limit, failed). Es la vista del lado
// "destino": cuántos clips se publicaron y cuántos están en backoff por
// plataforma.
func GetPublicationStats(db *sql.DB) (map[string]map[string]int, error) {
	rows, err := db.Query(
		`SELECT platform, status, COUNT(*) FROM publications GROUP BY platform, status`,
	)
	if err != nil {
		return nil, fmt.Errorf("publication stats: %w", err)
	}
	defer rows.Close()

	out := make(map[string]map[string]int)
	for rows.Next() {
		var platform, status string
		var n int
		if err := rows.Scan(&platform, &status, &n); err != nil {
			return nil, fmt.Errorf("scan publication stats: %w", err)
		}
		if out[platform] == nil {
			out[platform] = make(map[string]int)
		}
		out[platform][status] = n
	}
	return out, rows.Err()
}

// GetJobStats devuelve la cantidad de jobs por tipo y estado
// (queued, running, done, error). Muestra la salud de la cola: queued alto y
// constante indica un worker parado; error alto indica fallos recurrentes.
func GetJobStats(db *sql.DB) (map[string]map[string]int, error) {
	rows, err := db.Query(
		`SELECT type, status, COUNT(*) FROM jobs GROUP BY type, status`,
	)
	if err != nil {
		return nil, fmt.Errorf("job stats: %w", err)
	}
	defer rows.Close()

	out := make(map[string]map[string]int)
	for rows.Next() {
		var jtype, status string
		var n int
		if err := rows.Scan(&jtype, &status, &n); err != nil {
			return nil, fmt.Errorf("scan job stats: %w", err)
		}
		if out[jtype] == nil {
			out[jtype] = make(map[string]int)
		}
		out[jtype][status] = n
	}
	return out, rows.Err()
}
