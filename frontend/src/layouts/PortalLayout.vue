<script setup lang="ts">
import Sidebar from '../components/Sidebar.vue'
import TopBar from '../components/TopBar.vue'
</script>

<template>
  <div class="portal">
    <Sidebar />
    <div class="portal__main">
      <TopBar />
      <slot />
    </div>
  </div>
</template>

<style scoped>
.portal {
  height: 100%;
  display: flex;
}

.portal__main {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
}
</style>
