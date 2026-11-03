import { mount } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { describe, expect, it } from 'vitest'

import BreakdownPanel from './BreakdownPanel.vue'
import MetricCard from './MetricCard.vue'

function mountWithElement(component: Parameters<typeof mount>[0], props: Record<string, unknown>) {
  return mount(component, {
    props,
    global: { plugins: [ElementPlus] },
  })
}

describe('MetricCard', () => {
  it('labels an improvement without relying on color alone', () => {
    const wrapper = mountWithElement(MetricCard, {
      label: '错误率',
      value: '2.50%',
      delta: -3.2,
      deltaKind: 'pp',
      direction: 'down',
    })

    expect(wrapper.text()).toContain('↓ 3.2 个百分点')
    expect(wrapper.text()).toContain('低于上期')
    expect(wrapper.find('.metric-delta').classes()).toContain('is-good')
  })

  it('explains when the previous period has no data', () => {
    const wrapper = mountWithElement(MetricCard, {
      label: '请求量',
      value: '0',
    })

    expect(wrapper.text()).toContain('上期无数据')
  })
})

describe('BreakdownPanel', () => {
  it('renders an accessible empty state', () => {
    const wrapper = mountWithElement(BreakdownPanel, {
      title: '上游分布',
      rows: [],
      emptyText: '所选范围内无上游调用',
    })

    expect(wrapper.text()).toContain('所选范围内无上游调用')
  })

  it('shows request, token and error context per row', () => {
    const wrapper = mountWithElement(BreakdownPanel, {
      title: '上游分布',
      rows: [
        {
          key: 'provider-1',
          label: 'deepseek',
          value: 20,
          tokens: 1234,
          errors: 2,
          latencyMs: 850,
        },
      ],
      emptyText: '无数据',
    })

    expect(wrapper.text()).toContain('deepseek')
    expect(wrapper.text()).toContain('1,234')
    expect(wrapper.text()).toContain('10.0%')
    expect(wrapper.find('[role="img"]').attributes('aria-label')).toContain(
      '20 次请求',
    )
  })
})
