package service

import "context"

// Only New API task cleanup/outbox delivery belongs to this lifecycle. Native
// video polling and settlement are started separately by the host application.
func (s *GenerationService) startHostMaintenance(ctx context.Context) {
	repo, plugins, accounts, cache := s.repo, s.plugins, s.accounts, s.cache
	if quotaRepo, ok := repo.(GenerationQuotaOutboxRepository); ok && cache != nil {
		s.wg.Add(1)
		go s.workGenerationQuota(ctx, quotaRepo)
	}
	if fileRepo, ok := repo.(GenerationFileRepository); ok {
		s.wg.Add(1)
		go s.workGenerationFileCleanup(ctx, fileRepo)
	}
	if preparationRepo, ok := repo.(GenerationPreparationRepository); ok && plugins != nil && accounts != nil {
		s.wg.Add(1)
		go s.workPreparationCleanup(ctx, preparationRepo)
	}
}
