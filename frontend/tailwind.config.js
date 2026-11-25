/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{vue,js,ts,jsx,tsx}', './packages/newapi-ui/src/**/*.{vue,ts}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        // 主色调 - 墨玉（Ink & Jade）。刻意压低饱和度：
        // 原来的 #14b8a6 / #0d9488 是典型的「AI 产品绿」，
        // 满屏出现时会显廉价，这里统一换成带灰的墨玉色阶。
        primary: {
          50: '#f2f6f4',
          100: '#e2ece8',
          200: '#c6d9d3',
          300: '#a3c3ba',
          400: '#7ba79b',
          500: '#5d8b7f',
          600: '#3f635a',
          700: '#35564e',
          800: '#2a473f',
          900: '#213832',
          950: '#111f1b'
        },
        // 辅助色 - 深蓝灰
        accent: {
          50: '#f8fafc',
          100: '#f1f5f9',
          200: '#e2e8f0',
          300: '#cbd5e1',
          400: '#94a3b8',
          500: '#64748b',
          600: '#475569',
          700: '#334155',
          800: '#1e293b',
          900: '#0f172a',
          950: '#020617'
        },
        // 中性暖灰：覆盖 Tailwind 默认 gray（默认灰带明显蓝调）。
        // 全站用了 1 万多次 gray-*，它才是「界面发冷」的真正底色来源；
        // 换成同一明度的暖灰后，对比度不变（±0.3），色温整体回正。
        gray: {
          50: '#faf9f7',
          100: '#f3f1ee',
          200: '#e6e3de',
          300: '#d2cec7',
          400: '#a49f97',
          500: '#726d66',
          600: '#54504a',
          700: '#3f3c37',
          800: '#2a2825',
          900: '#1a1917',
          950: '#0f0e0d'
        },
        // 墨玉 / 古铜 / 陶土：仪表盘图标的唯一三支色，
        // 取代原来 blue/green/purple/amber/indigo/violet/rose 的糖果配色。
        jade: {
          50: '#f2f6f4',
          100: '#e2ece8',
          200: '#c6d9d3',
          300: '#a3c3ba',
          400: '#7ba79b',
          500: '#5d8b7f',
          600: '#3f635a',
          700: '#35564e',
          800: '#2a473f',
          900: '#213832',
          950: '#111f1b'
        },
        bronze: {
          50: '#f8f5ef',
          100: '#efe8d9',
          200: '#ded1b6',
          300: '#c9b48c',
          400: '#b4915f',
          500: '#9c7742',
          600: '#7d5c31',
          700: '#644a29',
          800: '#4f3a23',
          900: '#3c2d1c',
          950: '#21180f'
        },
        clay: {
          50: '#f8f2f1',
          100: '#eee1df',
          200: '#dcc3c0',
          300: '#c69d99',
          400: '#ad7b76',
          500: '#94605b',
          600: '#7a4b47',
          700: '#623e3a',
          800: '#4d3330',
          900: '#3a2725',
          950: '#1f1513'
        },
        // 深色模式背景：中性墨（不再是 slate 蓝灰）
        // 原来的 #1e293b / #0f172a 带明显蓝调，与暖纸底色和墨玉强调色冲突，
        // 深色主题下整片界面会发紫发蓝。这里换成同一色相的近黑。
        dark: {
          50: '#f4f5f3',
          100: '#e6e8e5',
          200: '#cfd2cd',
          300: '#adb2ab',
          400: '#8b918a',
          500: '#6b716b',
          600: '#4f544f',
          700: '#3a3e3a',
          800: '#242725',
          900: '#171917',
          950: '#0d0f0d'
        }
      },
      fontFamily: {
        sans: [
          'system-ui',
          '-apple-system',
          'BlinkMacSystemFont',
          'Segoe UI',
          'Roboto',
          'Helvetica Neue',
          'Arial',
          'PingFang SC',
          'Hiragino Sans GB',
          'Microsoft YaHei',
          'sans-serif'
        ],
        mono: ['ui-monospace', 'SFMono-Regular', 'Menlo', 'Monaco', 'Consolas', 'monospace']
      },
      boxShadow: {
        glass: '0 8px 32px rgba(0, 0, 0, 0.08)',
        'glass-sm': '0 4px 16px rgba(0, 0, 0, 0.06)',
        glow: '0 0 20px rgba(63, 99, 90, 0.22)',
        'glow-lg': '0 0 40px rgba(63, 99, 90, 0.3)',
        card: '0 1px 3px rgba(0, 0, 0, 0.04), 0 1px 2px rgba(0, 0, 0, 0.06)',
        'card-hover': '0 10px 40px rgba(0, 0, 0, 0.08)',
        'inner-glow': 'inset 0 1px 0 rgba(255, 255, 255, 0.1)'
      },
      backgroundImage: {
        'gradient-radial': 'radial-gradient(var(--tw-gradient-stops))',
        'gradient-primary': 'linear-gradient(135deg, #5d8b7f 0%, #3f635a 100%)',
        'gradient-dark': 'linear-gradient(135deg, #1e293b 0%, #0f172a 100%)',
        'gradient-glass':
          'linear-gradient(135deg, rgba(255,255,255,0.1) 0%, rgba(255,255,255,0.05) 100%)',
        'mesh-gradient':
          'radial-gradient(at 40% 20%, rgba(93, 139, 127, 0.10) 0px, transparent 50%), radial-gradient(at 80% 0%, rgba(201, 168, 118, 0.07) 0px, transparent 50%), radial-gradient(at 0% 50%, rgba(93, 139, 127, 0.07) 0px, transparent 50%)'
      },
      animation: {
        'fade-in': 'fadeIn 0.3s ease-out',
        'slide-up': 'slideUp 0.3s ease-out',
        'slide-down': 'slideDown 0.3s ease-out',
        'slide-in-right': 'slideInRight 0.3s ease-out',
        'scale-in': 'scaleIn 0.2s ease-out',
        'pulse-slow': 'pulse 3s cubic-bezier(0.4, 0, 0.6, 1) infinite',
        shimmer: 'shimmer 2s linear infinite',
        glow: 'glow 2s ease-in-out infinite alternate'
      },
      keyframes: {
        fadeIn: {
          '0%': { opacity: '0' },
          '100%': { opacity: '1' }
        },
        slideUp: {
          '0%': { opacity: '0', transform: 'translateY(10px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' }
        },
        slideDown: {
          '0%': { opacity: '0', transform: 'translateY(-10px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' }
        },
        slideInRight: {
          '0%': { opacity: '0', transform: 'translateX(20px)' },
          '100%': { opacity: '1', transform: 'translateX(0)' }
        },
        scaleIn: {
          '0%': { opacity: '0', transform: 'scale(0.95)' },
          '100%': { opacity: '1', transform: 'scale(1)' }
        },
        shimmer: {
          '0%': { backgroundPosition: '-200% 0' },
          '100%': { backgroundPosition: '200% 0' }
        },
        glow: {
          '0%': { boxShadow: '0 0 20px rgba(20, 184, 166, 0.25)' },
          '100%': { boxShadow: '0 0 30px rgba(20, 184, 166, 0.4)' }
        }
      },
      backdropBlur: {
        xs: '2px'
      },
      borderRadius: {
        '4xl': '2rem'
      }
    }
  },
  plugins: []
}
