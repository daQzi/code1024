<template>
  <AppLayout>
    <div class="space-y-5">
      <RouterLink to="/admin/plugins" class="btn btn-secondary">{{ t('admin.plugins.center.back') }}</RouterLink>
      <NativePluginsPanel />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import NativePluginsPanel from '@/components/admin/NativePluginsPanel.vue'
const { t } = useI18n()
</script>
