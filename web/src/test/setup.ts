import { config } from '@vue/test-utils'
import { defineComponent } from 'vue'

// Element Plus 2.14's ElSelect enters a recursive update loop under jsdom
// (the same minimal select reproduces it), which Vitest reports as unhandled
// errors and attributes to later tests. These lightweight stubs keep DOM
// tests deterministic; select interactions are covered by E2E tests instead.
const ElSelectStub = defineComponent({
  name: 'ElSelect',
  template: '<div class="el-select-stub"><slot /></div>',
})

const ElOptionStub = defineComponent({
  name: 'ElOption',
  props: {
    label: { type: String, default: '' },
  },
  template: '<span class="el-option-stub">{{ label }}</span>',
})

config.global.stubs = {
  ElSelect: ElSelectStub,
  ElOption: ElOptionStub,
}
