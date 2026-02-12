<template>
	<div class="container" >
		<h1>Hello </h1>
		< button @click="handleClick" > Click me </button>
			< div >
			<span>what is this </span>
				</div>
				</div>
				</template>

				< script setup >
	import { ref } from 'vue'

const title = ref('Hello from Vue!')

function handleClick() {
	console.log('Button clicked')
}
</script>

	< style scoped >
		.container {
	padding: 20px;
	background - color: #f0f0f0;
}

		h1 {
	color: #333;
}

		button {
	padding: 10px 20px;
	cursor: pointer;
}
</style>
