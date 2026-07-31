<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import PortalLayout from '../layouts/PortalLayout.vue'
import DropZone from '../components/DropZone.vue'
import FileGrid from '../components/FileGrid.vue'
import { useFilesStore } from '../stores/files'
import { AlertCircle } from '@lucide/vue'

const files = useFilesStore()
const route = useRoute()
const router = useRouter()
const connectError = ref<string | null>(null)

onMounted(() => {
  files.refresh()

  const err = route.query.connect_error
  if (typeof err === 'string' && err) {
    connectError.value = err
    router.replace({ query: { ...route.query, connect_error: undefined } })
  }
})
</script>

<template>
  <PortalLayout>
    <div
      v-if="connectError"
      class="border-destructive/40 bg-destructive/10 text-destructive mx-6 mt-4 flex items-center justify-between gap-2 rounded-lg border px-4 py-2.5 text-sm"
    >
      <span class="flex items-center gap-2">
        <AlertCircle class="size-4 shrink-0" />
        Couldn't connect that account: {{ connectError }}
      </span>
      <button class="shrink-0 opacity-70 hover:opacity-100" @click="connectError = null">×</button>
    </div>
    <DropZone>
      <FileGrid />
    </DropZone>
  </PortalLayout>
</template>
