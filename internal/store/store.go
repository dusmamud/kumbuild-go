package store

import (
	"context"
	"kumbuild-go/internal/models"
)

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
	GetAllCustomerEmails(ctx context.Context) ([]string, error)
	Close()
}
