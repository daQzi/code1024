<template>
  <BaseDialog
    :show="billing?.schema_version === 1"
    :title="t('usage.toolBillingDetails')"
    width="wide"
    @close="$emit('close')"
  >
    <div v-if="billing" class="space-y-4" data-testid="provider-tool-billing-details">
      <p class="break-all text-xs text-gray-500 dark:text-dark-400">{{ requestId }}</p>
      <p class="text-sm text-gray-600 dark:text-dark-300">{{ t('usage.toolBillingSnapshotHint') }}</p>
      <div class="overflow-x-auto">
        <table class="w-full text-left text-xs sm:text-sm">
          <thead class="border-b border-gray-200 text-xs text-gray-500 dark:border-dark-700 dark:text-dark-400">
            <tr>
              <th scope="col" class="px-2 py-2 font-medium">{{ t('usage.toolName') }}</th>
              <th scope="col" class="whitespace-nowrap px-2 py-2 text-right font-medium">{{ t('usage.toolCalls') }}</th>
              <th scope="col" class="whitespace-nowrap px-2 py-2 text-right font-medium">{{ t('usage.toolPricePer1k') }}</th>
              <th scope="col" class="whitespace-nowrap px-2 py-2 text-right font-medium">{{ t('usage.toolBaseCost') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 text-gray-700 dark:divide-dark-700 dark:text-dark-200">
            <tr v-for="item in billing.items" :key="item.name" data-testid="provider-tool-billing-item">
              <th scope="row" class="max-w-56 break-all px-2 py-3 font-medium">{{ item.name }}</th>
              <td class="px-2 py-3 text-right tabular-nums">{{ item.count }}</td>
              <td class="whitespace-nowrap px-2 py-3 text-right tabular-nums">
                {{ item.price_per_1k == null ? '—' : `$${item.price_per_1k.toFixed(8)}` }}
              </td>
              <td class="whitespace-nowrap px-2 py-3 text-right tabular-nums">${{ item.cost_usd.toFixed(8) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <dl class="space-y-2 border-t border-gray-200 pt-3 text-sm dark:border-dark-700">
        <div class="flex items-center justify-between gap-4">
          <dt class="text-gray-500 dark:text-dark-400">{{ t('usage.toolRateMultiplier') }}</dt>
          <dd class="text-gray-800 dark:text-dark-100">×{{ billing.rate_multiplier }}</dd>
        </div>
        <div class="flex items-center justify-between gap-4">
          <dt class="text-gray-500 dark:text-dark-400">{{ t('usage.toolSubtotal') }}</dt>
          <dd class="font-medium tabular-nums text-green-600 dark:text-green-400">${{ billing.actual_usd.toFixed(8) }}</dd>
        </div>
      </dl>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { ProviderToolBilling } from '@/types'

defineProps<{ billing: ProviderToolBilling | null; requestId: string }>()
defineEmits<{ close: [] }>()
const { t } = useI18n()
</script>
