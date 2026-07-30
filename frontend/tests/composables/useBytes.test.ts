import { describe, expect, it } from 'vitest'
import { formatBytes, usageLevel } from '../../src/composables/useBytes'

describe('formatBytes', () => {
  it('renders small byte counts plainly', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(512)).toBe('512 B')
  })

  it('renders KiB/MiB/GiB with one decimal place', () => {
    expect(formatBytes(1024)).toBe('1.0 KiB')
    expect(formatBytes(1536)).toBe('1.5 KiB')
    expect(formatBytes(1024 * 1024 * 5)).toBe('5.0 MiB')
    expect(formatBytes(1024 * 1024 * 1024 * 2)).toBe('2.0 GiB')
  })
})

describe('usageLevel', () => {
  it('classifies fractions into ok/warn/danger thresholds', () => {
    expect(usageLevel(0)).toBe('ok')
    expect(usageLevel(0.69)).toBe('ok')
    expect(usageLevel(0.7)).toBe('warn')
    expect(usageLevel(0.89)).toBe('warn')
    expect(usageLevel(0.9)).toBe('danger')
    expect(usageLevel(1)).toBe('danger')
  })
})
