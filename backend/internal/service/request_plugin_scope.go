package service

import "slices"

// Channel types use the pinned New API identities. Zero means that no channel
// has been selected, or that the platform has no equivalent identity.
func requestPluginMatchesChannelType(item RequestPluginInstance, channelType int) bool {
	if item.Version == nil || item.Version.Meta.Scope == nil || len(item.Version.Meta.Scope.ChannelTypes) == 0 {
		return true
	}
	return channelType > 0 && slices.Contains(item.Version.Meta.Scope.ChannelTypes, channelType)
}

func (p *RequestPluginPlan) earlyIngressItem(item RequestPluginInstance) bool {
	return item.Scope != "account" && slices.Contains(item.Stages, "ingress") && (p.dryRun || requestPluginMatchesChannelType(item, 0))
}

func (p *RequestPluginPlan) lateIngressItem(item RequestPluginInstance) bool {
	return slices.Contains(item.Stages, "ingress") && !p.ingressRan[item.ID]
}

func (p *RequestPluginPlan) hasLateIngressFor(accountID int64, channelType int) bool {
	return p != nil && slices.ContainsFunc(p.attemptItems(accountID, channelType), p.lateIngressItem)
}

func (p *RequestPluginPlan) itemMatchesChannel(item RequestPluginInstance, accountID int64, channelType int) bool {
	return (item.Scope != "account" || slices.Contains(item.AccountIDs, accountID)) && (p.dryRun || requestPluginMatchesChannelType(item, channelType))
}

func (p *RequestPluginPlan) hasAttemptStage(item RequestPluginInstance) bool {
	return slices.ContainsFunc(item.Stages, func(stage string) bool {
		return stage == "request" || stage == "outbound" || stage == "egress" || stage == "ingress" && p.lateIngressItem(item)
	})
}
