package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"kumbuild-go/internal/models"
)

type MemoryStore struct {
	mu          sync.RWMutex
	orders      []models.Order
	campaigns   []models.Campaign
	nextOrderID int64
	nextCampID  int64
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		orders:      make([]models.Order, 0),
		campaigns:   make([]models.Campaign, 0),
		nextOrderID: 1,
		nextCampID:  1,
	}
}

func (m *MemoryStore) Close() {}

func (m *MemoryStore) CreateOrder(_ context.Context, o *models.Order) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, existing := range m.orders {
		if existing.Reference == o.Reference {
			return ErrDuplicateReference
		}
	}

	now := time.Now().UTC()
	o.ID = m.nextOrderID
	m.nextOrderID++
	o.CreatedAt = now
	o.UpdatedAt = now
	if o.Status == "" {
		o.Status = "new"
	}

	m.orders = append(m.orders, *o)
	return nil
}

func (m *MemoryStore) ListOrders(_ context.Context, filter OrderFilter) ([]models.Order, int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var filtered []models.Order
	searchLower := strings.ToLower(strings.TrimSpace(filter.Search))

	for _, o := range m.orders {
		if filter.Status != "" && o.Status != filter.Status {
			continue
		}
		if searchLower != "" {
			nameMatch := strings.Contains(strings.ToLower(o.Name), searchLower)
			phoneMatch := strings.Contains(strings.ToLower(o.Phone), searchLower)
			busMatch := strings.Contains(strings.ToLower(o.BusinessName), searchLower)
			if !nameMatch && !phoneMatch && !busMatch {
				continue
			}
		}
		filtered = append(filtered, o)
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})

	total := len(filtered)
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	page := filter.Page
	if page <= 0 {
		page = 1
	}

	offset := (page - 1) * limit
	if offset > total {
		return []models.Order{}, total, nil
	}

	end := offset + limit
	if end > total {
		end = total
	}

	result := make([]models.Order, end-offset)
	copy(result, filtered[offset:end])
	return result, total, nil
}

func (m *MemoryStore) GetOrderByID(_ context.Context, id int64) (*models.Order, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, o := range m.orders {
		if o.ID == id {
			cp := o
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *MemoryStore) UpdateOrderStatus(_ context.Context, id int64, status string) (*models.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.orders {
		if m.orders[i].ID == id {
			m.orders[i].Status = status
			m.orders[i].UpdatedAt = time.Now().UTC()
			cp := m.orders[i]
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *MemoryStore) GetCustomers(_ context.Context) ([]models.Customer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	groups := make(map[string][]models.Order)
	for _, o := range m.orders {
		groups[o.Phone] = append(groups[o.Phone], o)
	}

	var customers []models.Customer
	for phone, orderList := range groups {
		sort.Slice(orderList, func(i, j int) bool {
			return orderList[i].CreatedAt.After(orderList[j].CreatedAt)
		})

		latest := orderList[0]
		var email *string
		for _, ord := range orderList {
			if ord.Email != nil && *ord.Email != "" {
				email = ord.Email
				break
			}
		}

		customers = append(customers, models.Customer{
			Name:          latest.Name,
			BusinessName:  latest.BusinessName,
			Phone:         phone,
			WhatsApp:      latest.WhatsApp,
			Email:         email,
			OrdersCount:   len(orderList),
			LastOrderDate: latest.CreatedAt,
		})
	}

	sort.Slice(customers, func(i, j int) bool {
		return customers[i].LastOrderDate.After(customers[j].LastOrderDate)
	})

	return customers, nil
}

func (m *MemoryStore) CreateCampaign(_ context.Context, c *models.Campaign) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	c.ID = m.nextCampID
	m.nextCampID++
	c.CreatedAt = time.Now().UTC()
	m.campaigns = append(m.campaigns, *c)
	return nil
}

func (m *MemoryStore) ListCampaigns(_ context.Context) ([]models.Campaign, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make([]models.Campaign, len(m.campaigns))
	copy(res, m.campaigns)

	sort.Slice(res, func(i, j int) bool {
		return res[i].CreatedAt.After(res[j].CreatedAt)
	})

	return res, nil
}

func (m *MemoryStore) UpdateCampaignCounts(_ context.Context, id int64, sent int, failed int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.campaigns {
		if m.campaigns[i].ID == id {
			m.campaigns[i].SentCount = sent
			m.campaigns[i].FailedCount = failed
			m.campaigns[i].Detail = fmt.Sprintf("Sent: %d, Failed: %d", sent, failed)
			if m.campaigns[i].RecipientsCount > 0 && sent+failed >= m.campaigns[i].RecipientsCount {
				if sent > 0 {
					m.campaigns[i].Status = "sent"
				} else {
					m.campaigns[i].Status = "failed"
				}
			}
			return nil
		}
	}
	return nil
}

func (m *MemoryStore) GetAllCustomerEmails(_ context.Context) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	seen := make(map[string]struct{})
	var emails []string
	for _, o := range m.orders {
		if o.Email != nil {
			trimmed := strings.TrimSpace(*o.Email)
			if trimmed != "" {
				if _, exists := seen[trimmed]; !exists {
					seen[trimmed] = struct{}{}
					emails = append(emails, trimmed)
				}
			}
		}
	}
	return emails, nil
}
