import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import FileGrid from '../../src/components/FileGrid.vue'
import { useFilesStore } from '../../src/stores/files'

beforeEach(() => {
  setActivePinia(createPinia())
})

describe('FileGrid', () => {
  it('shows an empty state with no files', () => {
    const wrapper = mount(FileGrid)
    expect(wrapper.text()).toContain('No files yet')
  })

  it('renders a row per file with a formatted size and its drive(s)', () => {
    const store = useFilesStore()
    store.files = [
      {
        name: 'docs/hello.txt',
        size: 2048,
        status: 'complete',
        modifiedAt: new Date().toISOString(),
        accounts: ['home-gmail', 'work-gmail'],
      },
    ]

    const wrapper = mount(FileGrid)
    expect(wrapper.text()).toContain('docs/hello.txt')
    expect(wrapper.text()).toContain('2.0 KiB')
    expect(wrapper.text()).toContain('home-gmail')
    expect(wrapper.text()).toContain('work-gmail')
    // "complete" files don't get a status badge — only unusual statuses do.
    expect(wrapper.text()).not.toContain('complete')
    expect(wrapper.text()).not.toContain('No files yet')
  })

  it('shows a status badge for a non-complete file', () => {
    const store = useFilesStore()
    store.files = [
      {
        name: 'partial.bin',
        size: 100,
        status: 'incomplete',
        modifiedAt: new Date().toISOString(),
        accounts: ['home-gmail'],
      },
    ]

    const wrapper = mount(FileGrid)
    expect(wrapper.text()).toContain('incomplete')
  })

  it('shows an in-progress upload with its own progress bar', () => {
    const store = useFilesStore()
    store.uploads = [{ id: 1, name: 'big-file.zip', fraction: 0.42 }]

    const wrapper = mount(FileGrid)
    expect(wrapper.text()).toContain('big-file.zip')
  })
})
