import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import DropZone from '../../src/components/DropZone.vue'
import { useFilesStore } from '../../src/stores/files'

beforeEach(() => {
  setActivePinia(createPinia())
})

// jsdom doesn't implement DataTransfer, so drag/drop events are built as
// plain Events with a dataTransfer property attached by hand — enough for
// DropZone's handlers, which only read .types and .files off it.
function dispatchDragEvent(type: string, files: File[]) {
  const event = new Event(type, { bubbles: true, cancelable: true }) as Event & {
    dataTransfer?: { files: File[]; types: string[] }
  }
  event.dataTransfer = { files, types: ['Files'] }
  window.dispatchEvent(event)
}

describe('DropZone', () => {
  it('shows the drop overlay on dragenter and hides it after drop', async () => {
    const wrapper = mount(DropZone, { slots: { default: '<div>content</div>' } })
    const file = new File(['hello'], 'hello.txt', { type: 'text/plain' })

    dispatchDragEvent('dragenter', [file])
    await wrapper.vm.$nextTick()
    expect(wrapper.text()).toContain('Drop files here to upload')

    dispatchDragEvent('drop', [file])
    await wrapper.vm.$nextTick()
    expect(wrapper.text()).not.toContain('Drop files here to upload')

    wrapper.unmount()
  })

  it('uploads every dropped file via the files store', async () => {
    const wrapper = mount(DropZone, { slots: { default: '<div>content</div>' } })
    const store = useFilesStore()
    const uploadMany = vi.spyOn(store, 'uploadMany').mockResolvedValue(undefined)

    const file = new File(['hello'], 'hello.txt', { type: 'text/plain' })
    dispatchDragEvent('drop', [file])
    await wrapper.vm.$nextTick()

    expect(uploadMany).toHaveBeenCalledTimes(1)
    expect(Array.from(uploadMany.mock.calls[0][0] as File[])).toEqual([file])

    wrapper.unmount()
  })
})
