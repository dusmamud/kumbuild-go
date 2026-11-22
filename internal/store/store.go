package store

import (
	"context"
	"errors"
	"kumbuild-go/internal/models"
)

var ErrDuplicateReference = errors.New("duplicate reference")

type OrderFilter struct {
	Status string
	Search string
	Page   int
	Limit  int
}

type Store interface {
	CreateOrder(ctx context.Context, o *models.Order) error
	ListOrders(ctx context.Context, filter OrderFilter) ([]models.Order, int, error)
	GetOrderByID(ctx context.Context, id int64) (*models.Order, error)
	UpdateOrderStatus(ctx context.Context, id int64, status string) (*models.Order, error)
	GetCustomers(ctx context.Context) ([]models.Customer, error)
	CreateCampaign(ctx context.Context, c *models.Campaign) error
	ListCampaigns(ctx context.Context) ([]models.Campaign, error)
	UpdateCampaignCounts(ctx context.Context, id int64, sent int, failed int) error
	GetAllCustomerEmails(ctx context.Context) ([]string, error)
	Close()
}
