package repositories

import (
	"context"
	"testing"

	"github.com/benenen/channel-plugin/internal/domain"
	"github.com/benenen/channel-plugin/internal/testutil"
)

func TestChannelBindingRepositoryCreateAndGet(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewChannelBindingRepository(db)
	ctx := context.Background()

	binding, err := repo.Create(ctx, domain.ChannelBinding{
		ID:          "bind_test1",
		UserID:      "usr_1",
		ChannelType: "wechat",
		Status:      domain.BindingStatusPending,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByID(ctx, binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.BindingStatusPending {
		t.Fatalf("unexpected status: %s", got.Status)
	}
}

func TestChannelBindingRepositoryUpdateStatus(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewChannelBindingRepository(db)
	ctx := context.Background()

	binding, _ := repo.Create(ctx, domain.ChannelBinding{
		ID:          "bind_test2",
		UserID:      "usr_1",
		ChannelType: "wechat",
		Status:      domain.BindingStatusPending,
	})
	binding.Status = domain.BindingStatusConfirmed
	got, err := repo.Update(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.BindingStatusConfirmed {
		t.Fatal("status not updated")
	}
}
