#include <stdio.h>
#include <stdlib.h>
#include <string.h>

int main(void) {
    int *p = NULL;
    int x;
    int *arr = malloc(10 * sizeof(int));

    printf("%d\n", x);

    *p = 5;

    strcpy((char *)arr, "This string is too long for the allocated memory");

    free(arr);
    free(arr);

    return 0;
    printf("This code is unreachable\n");
}