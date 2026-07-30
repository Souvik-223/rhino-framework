<script setup lang="ts">
import { onMounted } from 'vue'
import PortalLayout from '../layouts/PortalLayout.vue'
import DropZone from '../components/DropZone.vue'
import FileGrid from '../components/FileGrid.vue'
import { useFilesStore } from '../stores/files'

const files = useFilesStore()
onMounted(() => files.refresh())
</script>

<template>
  <PortalLayout>
    <DropZone>
      <FileGrid />
    </DropZone>
  </PortalLayout>
</template>
