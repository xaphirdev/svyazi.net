package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionAge = 8 * time.Hour
const sessionCookie = "svyazi_net_session"

var roles = map[string]string{"admin": "Администратор", "duty": "Дежурный", "engineer": "Инженер"}

type session struct {
	role     string
	expires  time.Time
	engineer engineer
}

type attempt struct {
	count int
	reset time.Time
}

type api struct {
	store    *store
	secure   bool
	mu       sync.Mutex
	sessions map[string]session
	attempts map[string]attempt
}

func newAPI(s *store) (*api, error) {
	a := &api{store: s, secure: os.Getenv("SVYAZI_NET_SECURE_COOKIE") == "1",
		sessions: make(map[string]session), attempts: make(map[string]attempt)}
	if err := s.bootstrapAdmin(context.Background()); err != nil {
		return nil, err
	}
	if err := s.bootstrapInitialData(context.Background(), time.Now()); err != nil {
		return nil, err
	}
	if err := s.exportPasswords(context.Background()); err != nil {
		return nil, err
	}
	return a, nil
}

func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write JSON: %v", err)
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, target any) error {
	return decodeBodyLimit(w, r, target, 32768)
}

func decodeBodyLimit(w http.ResponseWriter, r *http.Request, target any, limit int64) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return reject(415, "Ожидаются данные JSON.")
	}
	decode := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	err = decode.Decode(target)
	if err == nil {
		var extra json.RawMessage
		err = decode.Decode(&extra)
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return reject(413, "Данные формы слишком большие.")
	}
	return reject(400, "Некорректные данные JSON.")
}

func (a *api) sessionResponse(w http.ResponseWriter, record session) {
	role := record.role
	var value any
	if role != "" {
		value = role
	}
	label := roles[role]
	if record.engineer.ID != 0 {
		label = record.engineer.Name + " · " + record.engineer.Center
	}
	jsonResponse(w, 200, map[string]any{"role": value, "roleLabel": label, "engineer": role == "engineer" && record.engineer.ID != 0})
}

func (a *api) setCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: a.secure, MaxAge: maxAge})
}

func (a *api) login(w http.ResponseWriter, r *http.Request, oldToken string, now time.Time) error {
	var input struct{ Login, Password string }
	if err := decodeBody(w, r, &input); err != nil {
		return err
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	input.Login = strings.ToLower(strings.TrimSpace(input.Login))
	correct := false

	a.mu.Lock()
	defer a.mu.Unlock()
	for key, record := range a.sessions {
		if !now.Before(record.expires) {
			delete(a.sessions, key)
		}
	}
	for key, record := range a.attempts {
		if !now.Before(record.reset) {
			delete(a.attempts, key)
		}
	}
	record, exists := a.attempts[ip]
	if !exists {
		record.reset = now.Add(time.Minute)
	}
	if record.count >= 12 {
		return reject(429, "Слишком много попыток. Повторите через минуту.")
	}
	var person engineer
	role := input.Login
	if engineerLogin.MatchString(input.Login) && len(input.Password) <= 72 {
		var hash string
		person, hash, err = a.store.engineerCredentials(r.Context(), input.Login)
		if err != nil {
			return err
		}
		if person.ID != 0 {
			role = person.Role
			correct = bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.Password)) == nil
		}
	}
	if !correct {
		record.count++
		a.attempts[ip] = record
		return reject(401, "Неверный логин или пароль.")
	}

	if person.Role == "engineer" {
		role, err = a.store.effectiveRole(r.Context(), person.ID, now)
		if err != nil {
			return err
		}
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	token := hex.EncodeToString(random[:])
	delete(a.attempts, ip)
	delete(a.sessions, oldToken)
	newSession := session{role: role, expires: now.Add(sessionAge), engineer: person}
	a.sessions[token] = newSession
	a.setCookie(w, token, int(sessionAge/time.Second))
	a.sessionResponse(w, newSession)
	return nil
}

func (a *api) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if err := a.route(w, r); err != nil {
		var issue *problem
		if errors.As(err, &issue) {
			jsonResponse(w, issue.status, map[string]string{"error": issue.message})
			return
		}
		log.Printf("API %s %s: %v", r.Method, r.URL.Path, err)
		jsonResponse(w, 500, map[string]string{"error": "Ошибка при работе с данными."})
	}
}

func (a *api) route(w http.ResponseWriter, r *http.Request) error {
	ctx, now := r.Context(), time.Now()
	method, path := r.Method, r.URL.Path
	if (method == "POST" || method == "PUT" || method == "DELETE") && r.Header.Get("X-Svyazi-Net-Request") != "1" {
		return reject(403, "Недопустимый запрос.")
	}
	token := ""
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		token = cookie.Value
	}
	if method == "POST" && path == "/api/login" {
		return a.login(w, r, token, now)
	}
	if method == "POST" && path == "/api/logout" {
		a.mu.Lock()
		delete(a.sessions, token)
		a.mu.Unlock()
		a.setCookie(w, "", -1)
		a.sessionResponse(w, session{})
		return nil
	}
	a.mu.Lock()
	record := a.sessions[token]
	a.mu.Unlock()
	if !now.Before(record.expires) {
		record = session{}
	}
	if record.engineer.ID != 0 && record.engineer.Role == "engineer" {
		var err error
		record.role, err = a.store.effectiveRole(ctx, record.engineer.ID, now)
		if err != nil {
			return err
		}
	}
	role := record.role
	scopeID := record.engineer.ID
	if role == "duty" || role == "admin" {
		scopeID = 0
	}
	if method == "GET" && path == "/api/session" {
		a.sessionResponse(w, record)
		return nil
	}
	if role == "" {
		return reject(401, "Войдите в систему.")
	}
	if path == "/api/shifts" {
		return a.shifts(w, r, record, now)
	}
	if path == "/api/solutions" {
		return a.solutions(w, r, record, now)
	}
	if path == "/api/schedule" || strings.HasPrefix(path, "/api/schedule/") {
		return a.schedule(w, r, role)
	}
	if method == "GET" && path == "/api/reference/stations" {
		stations, err := a.store.stationReferences(ctx, scopeID)
		if err != nil {
			return err
		}
		jsonResponse(w, 200, stations)
		return nil
	}
	if path == "/api/engineers" {
		if role != "admin" {
			return reject(403, "Справочник инженеров доступен администратору.")
		}
		switch method {
		case "GET":
			people, err := a.store.engineers(ctx)
			if err != nil {
				return err
			}
			jsonResponse(w, 200, people)
			return nil
		case "POST":
			var input struct {
				engineer
				Password string `json:"password"`
			}
			if err := decodeBody(w, r, &input); err != nil {
				return err
			}
			person, err := a.store.addEngineer(ctx, input.engineer, input.Password)
			if err != nil {
				return err
			}
			jsonResponse(w, 201, person)
			return nil
		}
	}

	if method == "GET" && path == "/api/journal" {
		events, err := a.store.list(ctx, now, "", "", scopeID)
		if err != nil {
			return err
		}
		today := now.In(networkZone).Format(dateOnly)
		latest, err := a.store.latest(ctx, today, scopeID)
		if err != nil {
			return err
		}
		jsonResponse(w, 200, map[string]any{"events": events, "today": today, "latest": latest})
		return nil
	}
	if method == "GET" && (path == "/api/report" || path == "/api/report.csv" || path == "/api/report.xlsx") {
		from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
		if !validDate(from) || !validDate(to) || from > to {
			return reject(400, "Укажите корректный период. Начальная дата должна быть не позже конечной.")
		}
		events, err := a.store.list(ctx, now, from, to, scopeID)
		if err != nil {
			return err
		}
		switch path {
		case "/api/report.csv":
			writeCSV(w, events, from, to)
		case "/api/report.xlsx":
			return writeXLSX(w, events, from, to)
		default:
			jsonResponse(w, 200, map[string]any{"events": events})
		}
		return nil
	}
	if method == "POST" && path == "/api/incidents" {
		if role == "engineer" {
			return reject(403, "У инженера доступ только на просмотр.")
		}
		return a.save(w, r, 0, role == "admin")
	}
	if idText, found := strings.CutPrefix(path, "/api/incidents/"); method == "PUT" && found {
		id, err := strconv.ParseInt(idText, 10, 64)
		if err == nil && id > 0 && strconv.FormatInt(id, 10) == idText {
			if role != "admin" {
				return reject(403, "Редактирование доступно администратору.")
			}
			return a.save(w, r, id, true)
		}
	}
	return reject(404, "Раздел не найден.")
}

func validDate(value string) bool {
	return len(value) == 10 && normalizeStamp(value+"T00:00") != ""
}

func (a *api) save(w http.ResponseWriter, r *http.Request, id int64, canAssign bool) error {
	var input *draft
	if err := decodeBody(w, r, &input); err != nil {
		return err
	}
	if input == nil {
		return reject(400, "Некорректные данные.")
	}
	if !canAssign && input.EngineerID != nil {
		return reject(403, "Назначение инженера доступно администратору.")
	}
	saved, err := a.store.save(r.Context(), id, *input, time.Now())
	if err != nil {
		return err
	}
	status := http.StatusOK
	if id == 0 {
		status = http.StatusCreated
	}
	jsonResponse(w, status, map[string]int64{"id": saved})
	return nil
}

func routes(a *api, assets fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := a.store.db.PingContext(ctx); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("ok\n"))
	})
	mux.Handle("/api/", a)
	static := http.FileServer(http.FS(assets))
	// The asset filesystem also contains the import seed, which is private.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/index.html", "/app.js", "/styles.css", "/incident-model.mjs", "/frontend-assets/vue.esm-browser.prod.js":
			if r.Method != "GET" && r.Method != "HEAD" {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, "Method not allowed", 405)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			if strings.HasSuffix(r.URL.Path, ".js") || strings.HasSuffix(r.URL.Path, ".mjs") {
				w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			}
			static.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
	return mux
}
