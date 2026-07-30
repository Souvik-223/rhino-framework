<script setup lang="ts">
import Sidebar from '../components/Sidebar.vue'
import TopBar from '../components/TopBar.vue'
</script>

<template>
  <div class="flex h-svh">
    <div class="animate-fade-in-up flex h-full">
      <Sidebar />
    </div>
    <div class="bg-blueprint animate-fade-in-up flex min-w-0 flex-1 flex-col" style="animation-delay: 100ms">
      <TopBar />
      <slot />
    </div>
  </div>
</template>
