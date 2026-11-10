package platform

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type requestKey struct{}
type requestIdentity struct{ Key, Fingerprint string }

// RequireIdempotency validates the public write contract before any side effects.
// The legacy header remains an alias; conflicting aliases are rejected.
func RequireIdempotency(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodPatch {
				next.ServeHTTP(w, r)
				return
			}
			key, err := IdempotencyKey(r)
			if err != nil {
				Fail(w, err)
				return
			}
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
			if err != nil {
				Fail(w, Bad("Размер тела запроса не должен превышать 64 КиБ"))
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			// HMAC prevents offline guessing of contact or password fields from a receipt hash.
			fingerprint := KeyedHash(secret, r.Method+"\n"+r.URL.RequestURI()+"\n"+string(body))
			ctx := context.WithValue(r.Context(), requestKey{}, requestIdentity{Hash(key), fingerprint})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func IdempotencyKey(r *http.Request) (string, error) {
	x, old := r.Header.Get("X-Idempotency-Key"), r.Header.Get("Idempotency-Key")
	if x != "" && old != "" && x != old {
		return "", Bad("Заголовки ключа повтора противоречат друг другу")
	}
	if x == "" {
		x = old
	}
	id, err := uuid.Parse(x)
	if err != nil || id.Version() != 4 || id.String() != x {
		return "", Bad("X-Idempotency-Key должен быть случайным UUID v4")
	}
	return x, nil
}

func KeyedHash(secret, value string) string {
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write([]byte(value))
	return hex.EncodeToString(h.Sum(nil))
}

type receipt struct {
	Status  int             `json:"status"`
	Body    json.RawMessage `json:"body"`
	Headers http.Header     `json:"headers,omitempty"`
}

// Operation shares the business transaction: a receipt and its effects commit together.
// Callers must authenticate before loading a receipt and defer rolling back their tx.
type Operation struct {
	Tx                              pgx.Tx
	scope, key, fingerprint, secret string
	previous                        *receipt
	TTL                             time.Duration
	AfterCommit                     func()
	OrderID                         string
	SensitiveUntil                  time.Time
}

func LoadOperation(r *http.Request, tx pgx.Tx, scope, secret string) (*Operation, error) {
	identity, ok := r.Context().Value(requestKey{}).(requestIdentity)
	if !ok {
		return nil, Bad("Не задан ключ повтора операции")
	}
	o := &Operation{Tx: tx, scope: scope, key: identity.Key, fingerprint: identity.Fingerprint, secret: secret, TTL: 24 * time.Hour}
	// Same scope/key waits for the first transaction. A crashed process rolls back both.
	if _, err := tx.Exec(r.Context(), "SET LOCAL lock_timeout='3s'"); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(r.Context(), "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", scope+":"+identity.Key); err != nil {
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) && pgerr.Code == "55P03" {
			return nil, Conflict("request_in_progress", "Запрос ещё выполняется; повторите с тем же ключом")
		}
		return nil, err
	}
	if _, err := tx.Exec(r.Context(), "SET LOCAL lock_timeout=0"); err != nil {
		return nil, err
	}
	var fingerprint string
	var sealed []byte
	err := tx.QueryRow(r.Context(), "SELECT request_hash,response FROM api_requests WHERE scope=$1 AND key_hash=$2 AND expires_at>clock_timestamp()", scope, identity.Key).Scan(&fingerprint, &sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, nil
	}
	if err != nil {
		return nil, err
	}
	if fingerprint != identity.Fingerprint {
		return nil, Conflict("idempotency_conflict", "Ключ уже использован для другой операции или другого тела")
	}
	b, err := unseal(secret, sealed)
	if err != nil {
		return nil, err
	}
	var v receipt
	if err = json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	o.previous = &v
	return o, nil
}

func (o *Operation) BindOrder(id string, until time.Time) { o.OrderID = id; o.SensitiveUntil = until }

func (o *Operation) ReplayBody() json.RawMessage {
	if o.previous == nil {
		return nil
	}
	return o.previous.Body
}

func (o *Operation) Replay(w http.ResponseWriter, r *http.Request) bool {
	if o.previous == nil {
		return false
	}
	if err := o.Tx.Commit(r.Context()); err != nil {
		Fail(w, err)
		return true
	}
	for k, values := range o.previous.Headers {
		w.Header()[k] = values
	}
	w.Header().Set("Idempotency-Replayed", "true")
	JSON(w, o.previous.Status, o.previous.Body)
	return true
}

// Commit writes only successful receipts. Validation failures roll back and may be retried.
func (o *Operation) Commit(w http.ResponseWriter, r *http.Request, status int, v any) bool {
	body, err := json.Marshal(v)
	if err != nil {
		Fail(w, err)
		return false
	}
	headers := http.Header{}
	if values := w.Header().Values("Set-Cookie"); len(values) > 0 {
		headers["Set-Cookie"] = values
	}
	plain, err := json.Marshal(receipt{status, body, headers})
	if err != nil {
		Fail(w, err)
		return false
	}
	sealed, err := seal(o.secret, plain)
	if err != nil {
		Fail(w, err)
		return false
	}
	if o.OrderID != "" {
		_, err = o.Tx.Exec(r.Context(), `INSERT INTO api_requests(scope,key_hash,request_hash,response,expires_at,order_id,sensitive_until)
 VALUES($1,$2,$3,$4,LEAST(clock_timestamp()+$5::interval,$7),$6,$7)
 ON CONFLICT(scope,key_hash) DO UPDATE SET request_hash=excluded.request_hash,response=excluded.response,expires_at=excluded.expires_at,order_id=excluded.order_id,sensitive_until=excluded.sensitive_until`, o.scope, o.key, o.fingerprint, sealed, o.TTL.String(), o.OrderID, o.SensitiveUntil)
	} else {
		_, err = o.Tx.Exec(r.Context(), `INSERT INTO api_requests(scope,key_hash,request_hash,response,expires_at)
 VALUES($1,$2,$3,$4,clock_timestamp()+$5::interval)
 ON CONFLICT(scope,key_hash) DO UPDATE SET request_hash=excluded.request_hash,response=excluded.response,expires_at=excluded.expires_at`, o.scope, o.key, o.fingerprint, sealed, o.TTL.String())
	}
	if err == nil {
		err = o.Tx.Commit(r.Context())
	}
	if err != nil {
		w.Header().Del("Set-Cookie")
		Fail(w, err)
		return false
	}
	if o.AfterCommit != nil {
		o.AfterCommit()
	}
	JSON(w, status, json.RawMessage(body))
	return true
}

func receiptCipher(secret string) (cipher.AEAD, error) {
	key := sha256.Sum256([]byte("api-receipt:v1:" + secret))
	b, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}
func seal(secret string, plain []byte) ([]byte, error) {
	a, err := receiptCipher(secret)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return a.Seal(nonce, nonce, plain, []byte("api-receipt:v1")), nil
}
func unseal(secret string, sealed []byte) ([]byte, error) {
	a, err := receiptCipher(secret)
	if err != nil {
		return nil, err
	}
	if len(sealed) < a.NonceSize() {
		return nil, errors.New("invalid encrypted receipt")
	}
	return a.Open(nil, sealed[:a.NonceSize()], sealed[a.NonceSize():], []byte("api-receipt:v1"))
}

// RunReceiptCleanup enforces TTL physically as well as at read time, including after restart.
func RunReceiptCleanup(ctx context.Context, db interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, extra func(context.Context) error) {
	clean := func() {
		work, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		var err error
		for {
			var tag pgconn.CommandTag
			tag, err = db.Exec(work, "DELETE FROM api_requests WHERE (scope,key_hash) IN(SELECT scope,key_hash FROM api_requests WHERE expires_at<=clock_timestamp() ORDER BY expires_at LIMIT 500 FOR UPDATE SKIP LOCKED)")
			if err != nil || tag.RowsAffected() < 500 {
				break
			}
		}
		if err == nil && extra != nil {
			err = extra(work)
		}
		if err != nil && ctx.Err() == nil {
			slog.Warn("retention cleanup deferred")
		}
	}
	clean()
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			clean()
		}
	}
}
