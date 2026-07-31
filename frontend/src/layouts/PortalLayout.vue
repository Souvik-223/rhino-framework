<script setup lang="ts">
import AppSidebar from '../components/AppSidebar.vue'
import TopBar from '../components/TopBar.vue'
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar'
</script>

<template>
  <SidebarProvider class="h-svh">
    <div class="animate-fade-in-up flex h-full">
      <AppSidebar />
    </div>
    <SidebarInset class="bg-blueprint animate-fade-in-up min-w-0" style="animation-delay: 100ms">
      <TopBar />
      <slot />
    </SidebarInset>
  </SidebarProvider>
</template>
