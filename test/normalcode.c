#include <stdio.h>
#include <stdlib.h>

int main(void) {
    int *arr = malloc(100 * sizeof(int));
    if (arr == NULL) {
        return 1;
    }

    for (int i = 0; i < 100; i++) {
        arr[i] = i;
    }

    printf("OK\n");

    free(arr);
    return 0;
}