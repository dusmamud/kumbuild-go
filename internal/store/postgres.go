package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"kumbuild-go/internal/models"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(ctx context.Context, dbURL string) (*PostgresStore, error) {
	config, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, fmt.Errorf("parse db config: %w", err)
	}

	config.MaxConns = 10
	config.MinConns = 2
	config.MaxConnLifetime = 1 * time.Hour
	config.MaxConnIdleTime = 15 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create pgx pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}

	ps := &PostgresStore{pool: pool}
	if err := ps.runMigrations(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return ps, nil
}

func (s *PostgresStore) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

func (s *PostgresStore) runMigrations(ctx context.Context) error {
	// 1. Ensure schema_migrations exists
	_, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version VARCHAR(255) PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
	`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migration files: %w", err)
	}

	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)

	for _, file := range files {
		var exists bool
		err := s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)", file).Scan(&exists)
		if err != nil {
			return fmt.Errorf("check migration %s: %w", file, err)
		}
		if exists {
			continue
		}

		content, err := migrationFS.ReadFile("migrations/" + file)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", file, err)
		}

		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin tx for %s: %w", file, err)
		}

		if _, err := tx.Exec(ctx, string(content)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("exec migration %s: %w", file, err)
		}

		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", file); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %s: %w", file, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", file, err)
		}
	}

	return nil
}

func (s *PostgresStore) CreateOrder(ctx context.Context, o *models.Order) error {
	query := `
		INSERT INTO orders (reference, name, business_name, business_type, package, phone, whatsapp, email, content_language, message, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, created_at, updated_at;
	`
	now := time.Now().UTC()
	o.CreatedAt = now
	o.UpdatedAt = now
	if o.Status == "" {
		o.Status = "new"
	}

	err := s.pool.QueryRow(ctx, query,
		o.Reference,
		o.Name,
		o.BusinessName,
		o.BusinessType,
		o.Package,
		o.Phone,
		o.WhatsApp,
		o.Email,
		o.ContentLanguage,
		o.Message,
		o.Status,
		o.CreatedAt,
		o.UpdatedAt,
	).Scan(&o.ID, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrDuplicateReference
		}
		return err
	}
	return nil
}

func (s *PostgresStore) ListOrders(ctx context.Context, filter OrderFilter) ([]models.Order, int, error) {
	whereClauses := []string{"1=1"}
	args := []any{}
	argIdx := 1

	if filter.Status != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, filter.Status)
		argIdx++
	}

	if filter.Search != "" {
		pattern := "%" + strings.ToLower(filter.Search) + "%"
		whereClauses = append(whereClauses, fmt.Sprintf("(LOWER(name) LIKE $%d OR LOWER(phone) LIKE $%d OR LOWER(business_name) LIKE $%d)", argIdx, argIdx, argIdx))
		args = append(args, pattern)
		argIdx++
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM orders WHERE %s", whereSQL)
	var total int
	if err := s.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count orders: %w", err)
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	page := filter.Page
	if page <= 0 {
		page = 1
	}
	offset := (page - 1) * limit

	dataQuery := fmt.Sprintf(`
		SELECT id, reference, name, COALESCE(business_name, ''), COALESCE(business_type, ''), COALESCE(package, ''),
		       phone, whatsapp, email, COALESCE(content_language, ''), COALESCE(message, ''), status, created_at, updated_at
		FROM orders
		WHERE %s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d;
	`, whereSQL, argIdx, argIdx+1)

	args = append(args, limit, offset)

	rows, err := s.pool.Query(ctx, dataQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query orders: %w", err)
	}
	defer rows.Close()

	var orders []models.Order
	for rows.Next() {
		var o models.Order
		if err := rows.Scan(
			&o.ID,
			&o.Reference,
			&o.Name,
			&o.BusinessName,
			&o.BusinessType,
			&o.Package,
			&o.Phone,
			&o.WhatsApp,
			&o.Email,
			&o.ContentLanguage,
			&o.Message,
			&o.Status,
			&o.CreatedAt,
			&o.UpdatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan order: %w", err)
		}
		orders = append(orders, o)
	}

	if orders == nil {
		orders = []models.Order{}
	}

	return orders, total, nil
}

func (s *PostgresStore) GetOrderByID(ctx context.Context, id int64) (*models.Order, error) {
	query := `
		SELECT id, reference, name, COALESCE(business_name, ''), COALESCE(business_type, ''), COALESCE(package, ''),
		       phone, whatsapp, email, COALESCE(content_language, ''), COALESCE(message, ''), status, created_at, updated_at
		FROM orders
		WHERE id = $1;
	`
	var o models.Order
	err := s.pool.QueryRow(ctx, query, id).Scan(
		&o.ID,
		&o.Reference,
		&o.Name,
		&o.BusinessName,
		&o.BusinessType,
		&o.Package,
		&o.Phone,
		&o.WhatsApp,
		&o.Email,
		&o.ContentLanguage,
		&o.Message,
		&o.Status,
		&o.CreatedAt,
		&o.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("query order by id: %w", err)
	}
	return &o, nil
}

func (s *PostgresStore) UpdateOrderStatus(ctx context.Context, id int64, status string) (*models.Order, error) {
	query := `
		UPDATE orders
		SET status = $1, updated_at = NOW()
		WHERE id = $2
		RETURNING id, reference, name, COALESCE(business_name, ''), COALESCE(business_type, ''), COALESCE(package, ''),
		          phone, whatsapp, email, COALESCE(content_language, ''), COALESCE(message, ''), status, created_at, updated_at;
	`
	var o models.Order
	err := s.pool.QueryRow(ctx, query, status, id).Scan(
		&o.ID,
		&o.Reference,
		&o.Name,
		&o.BusinessName,
		&o.BusinessType,
		&o.Package,
		&o.Phone,
		&o.WhatsApp,
		&o.Email,
		&o.ContentLanguage,
		&o.Message,
		&o.Status,
		&o.CreatedAt,
		&o.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("update order status: %w", err)
	}
	return &o, nil
}

func (s *PostgresStore) GetCustomers(ctx context.Context) ([]models.Customer, error) {
	query := `
		SELECT DISTINCT ON (phone)
			name,
			COALESCE(business_name, '') as business_name,
			phone,
			whatsapp,
			email,
			COUNT(*) OVER (PARTITION BY phone) AS orders_count,
			MAX(created_at) OVER (PARTITION BY phone) AS last_order_date
		FROM orders
		ORDER BY phone, created_at DESC;
	`
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query customers: %w", err)
	}
	defer rows.Close()

	var customers []models.Customer
	for rows.Next() {
		var c models.Customer
		if err := rows.Scan(
			&c.Name,
			&c.BusinessName,
			&c.Phone,
			&c.WhatsApp,
			&c.Email,
			&c.OrdersCount,
			&c.LastOrderDate,
		); err != nil {
			return nil, fmt.Errorf("scan customer: %w", err)
		}
		customers = append(customers, c)
	}

	sort.Slice(customers, func(i, j int) bool {
		return customers[i].LastOrderDate.After(customers[j].LastOrderDate)
	})

	if customers == nil {
		customers = []models.Customer{}
	}

	return customers, nil
}

func (s *PostgresStore) CreateCampaign(ctx context.Context, c *models.Campaign) error {
	query := `
		INSERT INTO campaigns (subject, body_html, recipients_count, sent_count, failed_count, status, detail, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at;
	`
	now := time.Now().UTC()
	c.CreatedAt = now

	return s.pool.QueryRow(ctx, query,
		c.Subject,
		c.BodyHTML,
		c.RecipientsCount,
		c.SentCount,
		c.FailedCount,
		c.Status,
		c.Detail,
		c.CreatedAt,
	).Scan(&c.ID, &c.CreatedAt)
}

func (s *PostgresStore) ListCampaigns(ctx context.Context) ([]models.Campaign, error) {
	query := `
		SELECT id, subject, body_html, recipients_count, sent_count, failed_count, status, COALESCE(detail, ''), created_at
		FROM campaigns
		ORDER BY created_at DESC;
	`
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query campaigns: %w", err)
	}
	defer rows.Close()

	var campaigns []models.Campaign
	for rows.Next() {
		var c models.Campaign
		if err := rows.Scan(
			&c.ID,
			&c.Subject,
			&c.BodyHTML,
			&c.RecipientsCount,
			&c.SentCount,
			&c.FailedCount,
			&c.Status,
			&c.Detail,
			&c.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan campaign: %w", err)
		}
		campaigns = append(campaigns, c)
	}

	if campaigns == nil {
		campaigns = []models.Campaign{}
	}

	return campaigns, nil
}

func (s *PostgresStore) UpdateCampaignCounts(ctx context.Context, id int64, sent int, failed int) error {
	detail := fmt.Sprintf("Sent: %d, Failed: %d", sent, failed)
	status := "sending"

	var recipientsCount int
	_ = s.pool.QueryRow(ctx, "SELECT recipients_count FROM campaigns WHERE id = $1", id).Scan(&recipientsCount)
	if recipientsCount > 0 && sent+failed >= recipientsCount {
		if sent > 0 {
			status = "sent"
		} else {
			status = "failed"
		}
	}

	query := `
		UPDATE campaigns
		SET sent_count = $1, failed_count = $2, status = $3, detail = $4
		WHERE id = $5;
	`
	_, err := s.pool.Exec(ctx, query, sent, failed, status, detail, id)
	return err
}

func (s *PostgresStore) GetAllCustomerEmails(ctx context.Context) ([]string, error) {
	query := `
		SELECT DISTINCT email
		FROM orders
		WHERE email IS NOT NULL AND TRIM(email) != '';
	`
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query customer emails: %w", err)
	}
	defer rows.Close()

	var emails []string
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, fmt.Errorf("scan email: %w", err)
		}
		trimmed := strings.TrimSpace(email)
		if trimmed != "" {
			emails = append(emails, trimmed)
		}
	}
	return emails, nil
}
