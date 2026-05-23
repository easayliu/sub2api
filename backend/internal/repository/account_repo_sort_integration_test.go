//go:build integration

package repository

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (s *AccountRepoSuite) TestList_DefaultSortByNameAsc() {
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "z-account"})
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "a-account"})

	accounts, _, err := s.repo.List(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10})
	s.Require().NoError(err)
	s.Require().Len(accounts, 2)
	s.Require().Equal("a-account", accounts[0].Name)
	s.Require().Equal("z-account", accounts[1].Name)
}

func (s *AccountRepoSuite) TestListWithFilters_SortByPriorityDesc() {
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "low-priority", Priority: 10})
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "high-priority", Priority: 90})

	accounts, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{
		Page:      1,
		PageSize:  10,
		SortBy:    "priority",
		SortOrder: "desc",
	}, "", "", "", "", 0, "")
	s.Require().NoError(err)
	s.Require().Len(accounts, 2)
	s.Require().Equal("high-priority", accounts[0].Name)
	s.Require().Equal("low-priority", accounts[1].Name)
}

func (s *AccountRepoSuite) TestListWithFilters_SortByLastUsedDescNullsLast() {
	recent := time.Now().Add(-1 * time.Hour)
	older := time.Now().Add(-24 * time.Hour)
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "recently-used", LastUsedAt: &recent})
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "long-ago-used", LastUsedAt: &older})
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "never-used"})

	accounts, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{
		Page:      1,
		PageSize:  10,
		SortBy:    "last_used_at",
		SortOrder: "desc",
	}, "", "", "", "", 0, "")
	s.Require().NoError(err)
	s.Require().Len(accounts, 3)
	// DESC 时最近使用的在前，从未使用过（NULL）的应排在末尾而非最前。
	s.Require().Equal("recently-used", accounts[0].Name)
	s.Require().Equal("long-ago-used", accounts[1].Name)
	s.Require().Equal("never-used", accounts[2].Name)
}

func (s *AccountRepoSuite) TestListWithFilters_SortByLastUsedAscNullsLast() {
	recent := time.Now().Add(-1 * time.Hour)
	older := time.Now().Add(-24 * time.Hour)
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "recently-used", LastUsedAt: &recent})
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "long-ago-used", LastUsedAt: &older})
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "never-used"})

	accounts, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{
		Page:      1,
		PageSize:  10,
		SortBy:    "last_used_at",
		SortOrder: "asc",
	}, "", "", "", "", 0, "")
	s.Require().NoError(err)
	s.Require().Len(accounts, 3)
	// ASC 时最早使用的在前，从未使用过（NULL）的同样排在末尾。
	s.Require().Equal("long-ago-used", accounts[0].Name)
	s.Require().Equal("recently-used", accounts[1].Name)
	s.Require().Equal("never-used", accounts[2].Name)
}
