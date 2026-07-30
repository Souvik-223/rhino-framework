<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useFilesStore } from '../stores/files'

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
  <div class="drop-zone">
    <slot />

    <div v-if="dragging" class="drop-zone__overlay">
      <div class="drop-zone__message">Drop files here to upload</div>
    </div>

    <label class="drop-zone__picker btn btn-primary">
      + Upload
      <input type="file" multiple class="drop-zone__input" @change="onFilePicked" />
    </label>
  </div>
</template>

<style scoped>
.drop-zone {
  position: relative;
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.drop-zone__overlay {
  position: fixed;
  inset: 0;
  background: color-mix(in srgb, var(--accent) 12%, transparent);
  border: 3px dashed var(--accent);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 100;
  pointer-events: none;
}

.drop-zone__message {
  font-size: 1.5rem;
  font-weight: 600;
  color: var(--accent);
  background: var(--bg);
  padding: 1rem 2rem;
  border-radius: var(--radius);
}

.drop-zone__picker {
  position: absolute;
  bottom: 1.5rem;
  right: 1.5rem;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.2);
}

.drop-zone__input {
  display: none;
}
</style>
