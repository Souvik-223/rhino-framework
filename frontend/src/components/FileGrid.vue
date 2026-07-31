<script setup lang="ts">
import { ref } from 'vue'
import { useFilesStore } from '../stores/files'
import { api, type VirtualFile } from '../api/client'
import { formatBytes } from '../composables/useBytes'
import { Progress } from '@/components/ui/progress'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Download, Trash2, HardDrive, FileX, AlertCircle, AlertTriangle } from '@lucide/vue'

const files = useFilesStore()
const pendingDelete = ref<VirtualFile | null>(null)

function download(file: VirtualFile) {
  window.location.href = api.downloadUrl(file.name)
}

async function confirmDelete() {
  if (!pendingDelete.value) return
  await files.remove(pendingDelete.value.name)
  pendingDelete.value = null
}
</script>

<template>
  <div class="flex-1 overflow-y-auto p-6">
    <TransitionGroup
      v-if="files.uploads.length"
      tag="div"
      class="mb-5 flex flex-col gap-2"
      enter-active-class="transition-all duration-300"
      enter-from-class="opacity-0 -translate-y-2"
      leave-active-class="transition-all duration-300 absolute"
      leave-to-class="opacity-0"
    >
      <div
        v-for="u in files.uploads"
        :key="u.id"
        class="bg-card border-border/60 flex items-center gap-3 rounded-lg border p-3"
      >
        <span class="w-40 shrink-0 truncate text-sm font-medium">{{ u.name }}</span>
        <Progress
          :model-value="u.fraction * 100"
          class="h-2 flex-1"
          :indicator-class="u.error ? 'bg-destructive' : 'bg-primary bg-shimmer animate-shimmer'"
        />
        <span v-if="!u.error" class="text-muted-foreground w-10 shrink-0 text-right font-mono text-xs">
          {{ Math.round(u.fraction * 100) }}%
        </span>
        <span v-else class="text-destructive flex shrink-0 items-center gap-1 text-xs">
          <AlertCircle class="size-3.5" />
          {{ u.error }}
        </span>
        <button
          v-if="u.error"
          class="text-muted-foreground hover:text-foreground shrink-0"
          @click="files.dismissUpload(u.id)"
        >
          ×
        </button>
      </div>
    </TransitionGroup>

    <div v-if="files.files.length" class="overflow-hidden rounded-xl border border-border/60">
      <table class="w-full text-sm">
        <thead>
          <tr class="bg-secondary/50 text-muted-foreground text-xs tracking-wide uppercase">
            <th class="px-4 py-2.5 text-left font-medium">Name</th>
            <th class="px-4 py-2.5 text-left font-medium">Drive(s)</th>
            <th class="px-4 py-2.5 text-left font-medium">Size</th>
            <th class="px-4 py-2.5 text-left font-medium">Modified</th>
            <th class="px-4 py-2.5 text-right font-medium">Actions</th>
          </tr>
        </thead>
        <TransitionGroup
          tag="tbody"
          enter-active-class="transition-all duration-300"
          enter-from-class="opacity-0"
          leave-active-class="transition-all duration-200 absolute"
          leave-to-class="opacity-0"
        >
          <tr
            v-for="f in files.files"
            :key="f.name"
            class="group border-border/60 hover:bg-accent/40 border-t transition-colors"
          >
            <td class="max-w-0 px-4 py-3">
              <div class="truncate font-medium">{{ f.name }}</div>
              <div class="mt-1 flex flex-wrap gap-1">
                <Badge v-if="f.status !== 'complete'" variant="outline" class="text-[10px]">
                  {{ f.status }}
                </Badge>
                <Badge
                  v-if="f.degraded"
                  variant="destructive"
                  class="gap-1 text-[10px]"
                  title="The drive holding one or more chunks of this file was disconnected. Reconnect that same Google account to restore access."
                >
                  <AlertTriangle class="size-3" />
                  unavailable
                </Badge>
              </div>
            </td>
            <td class="px-4 py-3">
              <div class="flex flex-wrap gap-1">
                <Badge
                  v-for="drive in f.accounts"
                  :key="drive"
                  :variant="drive === 'disconnected' ? 'destructive' : 'secondary'"
                  class="gap-1 font-mono text-[10px] font-normal"
                >
                  <HardDrive class="size-3" />
                  {{ drive }}
                </Badge>
              </div>
            </td>
            <td class="text-muted-foreground px-4 py-3 font-mono text-xs">{{ formatBytes(f.size) }}</td>
            <td class="text-muted-foreground px-4 py-3 font-mono text-xs">
              {{ new Date(f.modifiedAt).toLocaleString() }}
            </td>
            <td class="px-4 py-3">
              <div class="flex justify-end gap-1 opacity-60 transition-opacity group-hover:opacity-100">
                <Button
                  variant="ghost"
                  size="icon"
                  class="size-8"
                  :disabled="f.degraded"
                  :title="f.degraded ? 'Unavailable — reconnect the disconnected drive to restore access' : 'Download'"
                  @click="download(f)"
                >
                  <Download class="size-4" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  class="text-destructive hover:text-destructive size-8"
                  title="Delete"
                  @click="pendingDelete = f"
                >
                  <Trash2 class="size-4" />
                </Button>
              </div>
            </td>
          </tr>
        </TransitionGroup>
      </table>
    </div>

    <div v-else-if="!files.loading" class="animate-fade-in-up mt-16 flex flex-col items-center gap-3 text-center">
      <div class="bg-secondary flex size-14 items-center justify-center rounded-full">
        <FileX class="text-muted-foreground size-6" />
      </div>
      <p class="text-muted-foreground text-sm">
        No files yet — drag and drop one anywhere on this page to upload it.
      </p>
    </div>

    <AlertDialog :open="!!pendingDelete" @update:open="(v) => !v && (pendingDelete = null)">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete "{{ pendingDelete?.name }}"?</AlertDialogTitle>
          <AlertDialogDescription>
            This deletes the encrypted chunks from your Drive accounts too — this can't be
            undone.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction class="bg-destructive hover:bg-destructive/90" @click="confirmDelete">
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>
