import { defineStore } from 'pinia'
import { ref } from 'vue'
import { toast } from 'vue-sonner'
import { api, type VirtualFile } from '../api/client'

interface UploadTask {
  id: number
  name: string
  fraction: number
  error?: string
}

let nextUploadId = 0

export const useFilesStore = defineStore('files', () => {
  const files = ref<VirtualFile[]>([])
  const loading = ref(false)
  const search = ref('')
  const uploads = ref<UploadTask[]>([])

  async function refresh() {
    loading.value = true
    try {
      files.value = await api.listFiles(search.value)
    } finally {
      loading.value = false
    }
  }

  async function upload(file: File) {
    const task: UploadTask = { id: nextUploadId++, name: file.name, fraction: 0 }
    uploads.value.push(task)
    try {
      await api.uploadFile(file, (fraction) => {
        task.fraction = fraction
      })
      uploads.value = uploads.value.filter((u) => u.id !== task.id)
      toast.success(`Uploaded "${file.name}"`)
      await refresh()
    } catch (err) {
      task.error = err instanceof Error ? err.message : 'upload failed'
      toast.error(`Failed to upload "${file.name}"`, { description: task.error })
    }
  }

  async function uploadMany(fileList: FileList | File[]) {
    await Promise.all(Array.from(fileList).map(upload))
  }

  function dismissUpload(id: number) {
    uploads.value = uploads.value.filter((u) => u.id !== id)
  }

  async function remove(name: string, purge: boolean) {
    await api.deleteFile(name, purge)
    toast.success(purge ? `Deleted "${name}" permanently` : `Removed "${name}"`)
    await refresh()
  }

  return { files, loading, search, uploads, refresh, upload, uploadMany, dismissUpload, remove }
})
