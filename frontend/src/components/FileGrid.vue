<script setup lang="ts">
import { useFilesStore } from '../stores/files'
import { api, type VirtualFile } from '../api/client'
import { formatBytes } from '../composables/useBytes'

const files = useFilesStore()

function download(file: VirtualFile) {
  window.location.href = api.downloadUrl(file.name)
}

async function remove(file: VirtualFile) {
  const purge = window.confirm(
    `Delete "${file.name}"?\n\nOK = also delete its remote chunks (--purge)\nCancel = keep remote chunks, just hide it`,
  )
  await files.remove(file.name, purge)
}
</script>

<template>
  <div class="file-grid">
    <div v-if="files.uploads.length" class="uploads">
      <div v-for="u in files.uploads" :key="u.id" class="upload">
        <span class="upload__name">{{ u.name }}</span>
        <div class="usage-bar">
          <div
            class="usage-bar__fill usage-bar__fill--ok"
            :style="{ width: `${u.fraction * 100}%` }"
          />
        </div>
        <span v-if="u.error" class="upload__error">{{ u.error }}</span>
        <button v-if="u.error" class="account__remove" @click="files.dismissUpload(u.id)">
          &times;
        </button>
      </div>
    </div>

    <table v-if="files.files.length" class="file-table">
      <thead>
        <tr>
          <th>Name</th>
          <th>Size</th>
          <th>Status</th>
          <th>Modified</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="f in files.files" :key="f.name">
          <td>{{ f.name }}</td>
          <td>{{ formatBytes(f.size) }}</td>
          <td>{{ f.status }}</td>
          <td>{{ new Date(f.modifiedAt).toLocaleString() }}</td>
          <td class="file-table__actions">
            <button class="btn" @click="download(f)">Download</button>
            <button class="btn btn-danger" @click="remove(f)">Delete</button>
          </td>
        </tr>
      </tbody>
    </table>

    <p v-else-if="!files.loading" class="file-grid__empty">
      No files yet — drag and drop one anywhere on this page to upload it.
    </p>
  </div>
</template>

<style scoped>
.file-grid {
  padding: 1rem;
  flex: 1;
  overflow-y: auto;
}

.uploads {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  margin-bottom: 1rem;
}

.upload {
  display: grid;
  grid-template-columns: 200px 1fr auto auto;
  align-items: center;
  gap: 0.75rem;
  font-size: 0.85rem;
}

.upload__name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.upload__error {
  color: var(--danger);
}

.file-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.9rem;
}

.file-table th {
  text-align: left;
  color: var(--text-dim);
  font-weight: 500;
  font-size: 0.78rem;
  text-transform: uppercase;
  letter-spacing: 0.03em;
  padding: 0.5rem;
  border-bottom: 1px solid var(--border);
}

.file-table td {
  padding: 0.6rem 0.5rem;
  border-bottom: 1px solid var(--border);
}

.file-table__actions {
  display: flex;
  gap: 0.5rem;
  justify-content: flex-end;
}

.file-grid__empty {
  color: var(--text-dim);
  margin-top: 3rem;
  text-align: center;
}
</style>
