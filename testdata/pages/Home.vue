<template>
  <div>
    <h1>{{ $t('pages.home.hello-world') }}</h1>
    <p>{{ $t('pages.home.welcome-to-our-application') }}</p>
    <button>{{ $t('pages.home.submit') }}</button>
    <span>{{ $t('pages.home.hello-world-2') }}</span>
  </div>
</template>

<script>
export default {
  name: 'Home'
}
</script>
