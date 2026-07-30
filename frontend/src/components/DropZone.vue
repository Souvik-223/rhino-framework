<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useFilesStore } from '../stores/files'
import { UploadCloud, Plus } from '@lucide/vue'

const files = useFilesStore()
const dragging = ref(false)
// dragenter/dragleave fire again for every child element the pointer
// crosses, so a plain boolean flickers off mid-drag — a depth counter
// only reaches zero when the pointer truly leaves the window.
let depth = 0

function onDragEnter(e: DragEvent) {
  e.preventDefault()
  depth++
  if (e.dataTransfer?.types.includes('Files')) dragging.value = true
}

function onDragOver(e: DragEvent) {
  e.preventDefault()
}

function onDragLeave(e: DragEvent) {
  e.preventDefault()
  depth = Math.max(0, depth - 1)
  if (depth === 0) dragging.value = false
}

function onDrop(e: DragEvent) {
  e.preventDefault()
  depth = 0
  dragging.value = false
  const dropped = e.dataTransfer?.files
  if (dropped && dropped.length) files.uploadMany(dropped)
}

onMounted(() => {
  window.addEventListener('dragenter', onDragEnter)
  window.addEventListener('dragover', onDragOver)
  window.addEventListener('dragleave', onDragLeave)
  window.addEventListener('drop', onDrop)
})
onUnmounted(() => {
  window.removeEventListener('dragenter', onDragEnter)
  window.removeEventListener('dragover', onDragOver)
  window.removeEventListener('dragleave', onDragLeave)
  window.removeEventListener('drop', onDrop)
})

function onFilePicked(e: Event) {
  const input = e.target as HTMLInputElement
  if (input.files?.length) files.uploadMany(input.files)
  input.value = ''
}
</script>

<template>
  <div class="relative flex min-h-0 flex-1 flex-col">
    <slot />

    <Transition
      enter-active-class="transition-all duration-200"
      enter-from-class="opacity-0 scale-95"
      leave-active-class="transition-all duration-150"
      leave-to-class="opacity-0 scale-95"
    >
      <div
        v-if="dragging"
        class="bg-background/90 fixed inset-0 z-50 flex items-center justify-center backdrop-blur-sm"
      >
        <div
          class="animate-pulse-ring border-primary bg-card flex flex-col items-center gap-3 rounded-2xl border-2 border-dashed px-16 py-14 shadow-2xl"
        >
          <UploadCloud class="text-primary size-10" />
          <p class="font-heading text-lg font-semibold tracking-tight">Drop files here to upload</p>
        </div>
      </div>
    </Transition>

    <label
      class="bg-primary text-primary-foreground hover:bg-primary/90 fixed right-8 bottom-8 flex cursor-pointer items-center gap-2 rounded-full px-5 py-3 text-sm font-medium shadow-lg shadow-black/10 transition-all hover:scale-105 hover:shadow-xl"
    >
      <Plus class="size-4" />
      Upload
      <input type="file" multiple class="hidden" @change="onFilePicked" />
    </label>
  </div>
</template>
