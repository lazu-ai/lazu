import BrandLogo from '../../brand/BrandLogo';

type ThemedBrandLogoProps = {
  alt: string;
  size: 'header' | 'footer';
};

// Both tones are in the markup and the theme class decides which one shows.
// The public chrome is also served as static HTML around the API-rendered
// /models pages, where no script picks a tone before paint.
const ThemedBrandLogo = ({ alt, size }: ThemedBrandLogoProps) => (
  <>
    <BrandLogo
      variant='primary'
      tone='graphite'
      size={size}
      alt={alt}
      className='lazu-logo--light'
    />
    <BrandLogo
      variant='primary'
      tone='paper'
      size={size}
      alt=''
      aria-hidden='true'
      className='lazu-logo--dark'
    />
  </>
);

export default ThemedBrandLogo;
