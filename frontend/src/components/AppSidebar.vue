<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useAccountsStore } from "../stores/accounts";
import { formatBytes, usageLevel } from "../composables/useBytes";
import { Progress } from "@/components/ui/progress";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
  SidebarSeparator,
  useSidebar,
} from "@/components/ui/sidebar";
import logoUrl from "../assets/logo.png";
import { HardDrive, Plus, X } from "@lucide/vue";

const store = useAccountsStore();
const newLabel = ref("");
const { state, isMobile, setOpen } = useSidebar();

onMounted(() => store.refresh());

const failedPhotos = ref(new Set<string>());
function onPhotoError(a: { label: string; photoUrl?: string }) {
  console.error(
    `Failed to load Google account photo for "${a.label}"`,
    a.photoUrl,
  );
  failedPhotos.value.add(a.label);
}
function showPhoto(a: { label: string; photoUrl?: string }) {
  return !!a.photoUrl && !failedPhotos.value.has(a.label);
}

const collapsed = computed(
  () => !isMobile.value && state.value === "collapsed",
);

const healthyCount = computed(
  () => store.accounts.filter((a) => !a.error).length,
);

function usedFraction(a: {
  limit?: number;
  usage?: number;
  unlimited?: boolean;
}) {
  if (a.unlimited || !a.limit) return 0;
  return (a.usage ?? 0) / a.limit;
}

const levelClasses: Record<"ok" | "warn" | "danger", string> = {
  ok: "bg-ok",
  warn: "bg-warn",
  danger: "bg-destructive",
};

function connectAccount() {
  if (collapsed.value) {
    setOpen(true);
    return;
  }
  if (!newLabel.value.trim()) return;
  store.connectAccount(newLabel.value.trim());
}
</script>

<template>
  <Sidebar collapsible="icon">
    <SidebarHeader>
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton
            size="lg"
            class="hover:bg-transparent active:bg-transparent cursor-default"
          >
            <img
              :src="logoUrl"
              alt="Rhino"
              class="size-7 shrink-0 object-contain"
            />
            <div class="grid flex-1 text-left leading-none">
              <span
                class="font-heading text-sidebar-foreground truncate text-base font-bold tracking-tight uppercase"
              >
                Rhino
              </span>
              <span
                class="font-heading text-sidebar-foreground/70 truncate text-[9px] font-semibold tracking-[0.2em] uppercase"
              >
                Merge
              </span>
            </div>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    </SidebarHeader>

    <SidebarSeparator />

    <SidebarContent>
      <SidebarGroup>
        <SidebarGroupLabel class="flex items-center justify-between pr-1">
          <span>Connected drives</span>
          <span class="text-muted-foreground font-mono text-[11px] normal-case">
            {{ healthyCount }}/{{ store.accounts.length }}
          </span>
        </SidebarGroupLabel>
        <SidebarGroupContent>
          <SidebarMenu class="gap-3">
            <SidebarMenuItem
              v-for="(a, i) in store.accounts"
              :key="a.label"
              :title="collapsed ? a.label : undefined"
              class="animate-fade-in-up"
              :style="{ animationDelay: `${i * 60}ms` }"
            >
              <div
                class="bg-card/60 group/account rounded-lg border border-border/60 transition-colors hover:border-border"
                :class="
                  collapsed ? 'flex size-8 items-center justify-center' : 'p-3'
                "
              >
                <template v-if="collapsed">
                  <img
                    v-if="showPhoto(a)"
                    :src="a.photoUrl"
                    :alt="a.label"
                    class="size-4 shrink-0 rounded-full object-cover"
                    @error="onPhotoError(a)"
                  />
                  <HardDrive
                    v-else
                    class="text-sidebar-primary size-4 shrink-0"
                  />
                </template>
                <template v-else>
                  <div class="flex items-center justify-between gap-2">
                    <div class="flex min-w-0 items-center gap-2">
                      <img
                        v-if="showPhoto(a)"
                        :src="a.photoUrl"
                        :alt="a.label"
                        class="size-4 shrink-0 rounded-full object-cover"
                        @error="onPhotoError(a)"
                      />
                      <HardDrive
                        v-else
                        class="text-sidebar-primary size-4 shrink-0"
                      />
                      <span class="truncate text-sm font-medium">{{
                        a.label
                      }}</span>
                    </div>
                    <button
                      class="text-muted-foreground hover:text-destructive shrink-0 opacity-0 transition-opacity group-hover/account:opacity-100"
                      title="Disconnect"
                      @click="store.removeAccount(a.label)"
                    >
                      <X class="size-3.5" />
                    </button>
                  </div>

                  <p v-if="a.error" class="text-destructive mt-1.5 text-xs">
                    unavailable: {{ a.error }}
                  </p>
                  <p
                    v-else-if="a.unlimited"
                    class="text-muted-foreground mt-1.5 text-xs"
                  >
                    unlimited
                  </p>
                  <template v-else>
                    <Progress
                      :model-value="Math.min(usedFraction(a) * 100, 100)"
                      class="mt-2 h-1.5"
                      :indicator-class="
                        levelClasses[usageLevel(usedFraction(a))]
                      "
                    />
                    <p
                      class="text-muted-foreground mt-1.5 font-mono text-[11px]"
                    >
                      {{ formatBytes(a.usage ?? 0) }} /
                      {{ formatBytes(a.limit ?? 0) }}
                    </p>
                  </template>
                </template>
              </div>
            </SidebarMenuItem>
          </SidebarMenu>

          <p
            v-if="!collapsed && !store.loading && store.accounts.length === 0"
            class="text-muted-foreground px-2 pt-1 text-sm"
          >
            No drives connected yet.
          </p>
        </SidebarGroupContent>
      </SidebarGroup>
    </SidebarContent>

    <SidebarSeparator />

    <SidebarFooter>
      <form class="flex flex-col gap-2" @submit.prevent="connectAccount">
        <Input
          v-if="!collapsed"
          v-model="newLabel"
          placeholder="e.g. home-gmail"
          class="h-8"
        />
        <Button
          type="submit"
          :size="collapsed ? 'icon-sm' : 'sm'"
          :class="!collapsed && 'w-full gap-1.5'"
          :title="collapsed ? 'Connect account' : undefined"
        >
          <Plus class="size-3.5" />
          <span v-if="!collapsed">Connect account</span>
        </Button>
      </form>
    </SidebarFooter>

    <SidebarRail />
  </Sidebar>
</template>
