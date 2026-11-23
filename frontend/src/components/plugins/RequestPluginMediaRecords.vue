<template>
  <details v-if="record.attachments?.length || record.media?.length" class="w-full rounded-lg border border-gray-200 px-3 py-2 text-xs dark:border-dark-700" data-media-records>
    <summary class="cursor-pointer font-medium text-gray-700 dark:text-gray-300">{{ t('requestPlugins.attachments') }}</summary>
    <p class="mt-2 text-gray-500">{{ t('requestPlugins.attachmentPrivacy') }}</p>
    <ul v-if="record.attachments?.length" class="mt-2 space-y-1">
      <li v-for="(attachment, index) in record.attachments" :key="index" class="flex flex-wrap gap-x-3 gap-y-1" data-attachment-record>
        <span>{{ index + 1 }} · {{ t(`requestPlugins.attachmentKinds.${attachment.kind}`) }}</span>
        <span class="font-mono text-gray-500">{{ attachment.mime || '—' }}</span>
        <span :class="attachment.status === 'checked' ? 'text-emerald-700 dark:text-emerald-400' : 'text-amber-700 dark:text-amber-400'">{{ t(`requestPlugins.attachmentStatuses.${attachment.status}`) }}</span>
        <span v-if="attachment.reason" class="text-gray-500">{{ attachmentReason(attachment.reason) }}</span>
        <span v-if="attachment.rewritten" class="text-emerald-700 dark:text-emerald-400">{{ t('requestPlugins.attachmentRewritten') }}</span>
      </li>
    </ul>
    <p v-if="record.more_attachments" class="mt-1 text-gray-500">{{ t('requestPlugins.moreAttachments', { count: record.more_attachments }) }}</p>
    <ul v-if="record.media?.length" class="mt-2 space-y-1">
      <li v-for="(media, index) in record.media" :key="index" class="flex flex-wrap gap-x-3 gap-y-1" data-image-record>
        <span>{{ t('requestPlugins.imageReencoded') }} #{{ media.index }}</span>
        <span class="font-mono">{{ media.from.mime }} {{ media.from.bytes.toLocaleString() }} B → {{ media.to.mime }} {{ media.to.bytes.toLocaleString() }} B</span>
        <span>{{ t(media.lossless ? 'requestPlugins.lossless' : 'requestPlugins.lossy') }}</span>
      </li>
    </ul>
  </details>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { RequestPluginRecord } from '@/api/admin/requestPlugins'

defineProps<{ record: Pick<RequestPluginRecord, 'attachments' | 'more_attachments' | 'media'> }>()
const { t } = useI18n()
const reasons = new Set(['unsupported', 'reference', 'too_large', 'unreadable', 'limit', 'busy', 'no_text', 'not_rewritable'])
const attachmentReason = (reason: string) => t(`requestPlugins.attachmentReasons.${reasons.has(reason) ? reason : 'unsupported'}`)
</script>
