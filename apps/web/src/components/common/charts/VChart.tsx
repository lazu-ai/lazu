import React, { type CSSProperties, useEffect, useMemo, useState } from 'react';
import { useActualTheme } from '../../../context/Theme';
import type { IVChartConstructor } from '@visactor/vchart';

type ChartSpec = Record<string, unknown> & {
  height?: CSSProperties['height'];
  width?: CSSProperties['width'];
};

export interface VChartProps {
  className?: string;
  option?: Record<string, unknown> & { theme?: string };
  spec?: ChartSpec;
  style?: CSSProperties;
}

type VChartRuntimeProps = VChartProps & {
  vchartConstrouctor: IVChartConstructor;
};

type VChartRuntime = {
  VChartCore: IVChartConstructor;
  VChartSimple: React.ForwardRefExoticComponent<
    VChartRuntimeProps & React.RefAttributes<HTMLDivElement>
  >;
};

let cachedVChartRuntime: VChartRuntime | null = null;
let cachedVChartRuntimePromise: Promise<VChartRuntime> | null = null;

const loadVChartRuntime = async (): Promise<VChartRuntime> => {
  if (cachedVChartRuntime) {
    return cachedVChartRuntime;
  }
  if (!cachedVChartRuntimePromise) {
    cachedVChartRuntimePromise = Promise.all([
      import('@visactor/react-vchart/esm/VChartSimple'),
      import('@visactor/vchart/esm/vchart-simple'),
    ])
      .then(([reactVChartModule, vchartCoreModule]) => {
        cachedVChartRuntime = {
          VChartSimple:
            reactVChartModule.VChartSimple as unknown as VChartRuntime['VChartSimple'],
          VChartCore: vchartCoreModule.default,
        };
        return cachedVChartRuntime;
      })
      .catch((error) => {
        cachedVChartRuntimePromise = null;
        throw error;
      });
  }
  return cachedVChartRuntimePromise;
};

const VChart = React.forwardRef<HTMLDivElement, VChartProps>((props, ref) => {
  const actualTheme = useActualTheme();
  const { option, spec, style, className, ...otherProps } = props;
  const [runtime, setRuntime] = useState(() => cachedVChartRuntime);
  const [loadFailed, setLoadFailed] = useState(false);
  const theme = actualTheme === 'dark' ? 'dark' : 'light';

  const chartOption = useMemo(
    () => ({
      ...(option || {}),
      theme: option?.theme || theme,
    }),
    [option, theme],
  );

  // VChart's built-in 'light' theme paints the canvas with a #ffffff
  // background, which clashes with our warm cream card surfaces (and
  // similarly the 'dark' theme paints near-black, missing the espresso
  // surface tone). Default the spec background to transparent so charts
  // inherit whatever container they sit in. Callers can still set their
  // own `background` to opt out.
  const mergedSpec = useMemo(
    () => (spec ? { background: 'transparent', ...spec } : spec),
    [spec],
  );

  const fallbackStyle = useMemo(
    () => ({
      width: spec?.width || style?.width || '100%',
      height: spec?.height || style?.height || '100%',
      minHeight: spec?.height || style?.height ? undefined : 64,
      ...style,
    }),
    [spec?.height, spec?.width, style],
  );

  useEffect(() => {
    if (runtime || loadFailed) {
      return undefined;
    }
    let cancelled = false;
    loadVChartRuntime()
      .then((nextRuntime) => {
        if (!cancelled) {
          setRuntime(nextRuntime);
        }
      })
      .catch((error) => {
        if (!cancelled) {
          console.error('[VChart] failed to load runtime', error);
          setLoadFailed(true);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [loadFailed, runtime]);

  if (!runtime) {
    return (
      <div
        ref={ref}
        className={className}
        style={fallbackStyle}
        aria-busy='true'
      />
    );
  }

  const { VChartCore, VChartSimple } = runtime;

  return (
    <VChartSimple
      ref={ref}
      {...otherProps}
      className={className}
      spec={mergedSpec}
      style={style}
      option={chartOption}
      vchartConstrouctor={VChartCore}
    />
  );
});

VChart.displayName = 'VChart';

export default VChart;
