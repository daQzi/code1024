<template>
  <main class="mx-auto max-w-2xl space-y-5 px-6 py-16">
    <h1 class="text-xl font-semibold">{{ t('admin.plugins.credential.callbackTitle') }}</h1>
    <p class="text-sm text-gray-500">{{ t('admin.plugins.credential.callbackHint') }}</p>
    <textarea v-if="callback" :value="callback" readonly rows="6" spellcheck="false" class="input font-mono text-xs" :aria-label="t('admin.plugins.credential.callbackURL')" @focus="($event.target as HTMLTextAreaElement).select()" />
    <p v-else class="text-sm text-gray-500">{{ t('admin.plugins.credential.callbackEmpty') }}</p>
  </main>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { takePluginLoginCallback } from '@/utils/pluginLoginCallback'
const { t } = useI18n()
const callback = ref(takePluginLoginCallback())
// Retain the one-time callback only in this page's memory. Never put the code
// in localStorage, an API request, an external link or a new history entry.
</script>
