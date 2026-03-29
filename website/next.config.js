/** @type {import('next').NextConfig} */
const nextConfig = {
  async rewrites() {
    return [
      {
        source: '/install',
        destination: '/api/install',
      },
    ];
  },
};

module.exports = nextConfig;
